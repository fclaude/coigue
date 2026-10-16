//! Diagnostics and semantic-token LSP client. The UI never touches server pipes.
//! This deliberately models only our small protocol subset, rather than an entire LSP SDK.
use serde::Deserialize;
use serde_json::{Value, json};
use std::{
    collections::VecDeque,
    io::{self, BufRead, BufReader, Read, Write},
    path::{Path, PathBuf},
    process::{Child, Command, Stdio},
    sync::{
        Arc, Mutex,
        mpsc::{self, Receiver, SyncSender, TryRecvError},
    },
    thread,
    time::{Duration, Instant},
};
use url::Url;

const TICK: Duration = Duration::from_millis(20);
const CHANGE_INTERVAL: Duration = Duration::from_millis(75);
const STARTUP_TIMEOUT: Duration = Duration::from_secs(20);
const SHUTDOWN_TIMEOUT: Duration = Duration::from_millis(500);
const TOKEN_TIMEOUT: Duration = Duration::from_secs(5);
const MAX_MESSAGE: usize = 16 * 1024 * 1024;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum Language {
    Rust,
    Go,
    Markdown,
}
impl Language {
    fn for_path(path: &Path) -> Option<Self> {
        match path.extension()?.to_str()?.to_ascii_lowercase().as_str() {
            "rs" => Some(Self::Rust),
            "go" => Some(Self::Go),
            "md" | "markdown" => Some(Self::Markdown),
            _ => None,
        }
    }
    fn id(self) -> &'static str {
        match self {
            Self::Rust => "rust",
            Self::Go => "go",
            Self::Markdown => "markdown",
        }
    }
    fn command(self) -> (&'static str, &'static [&'static str]) {
        match self {
            Self::Rust => ("rust-analyzer", &[]),
            Self::Go => ("gopls", &[]),
            Self::Markdown => ("marksman", &["server"]),
        }
    }
    fn root(self, path: &Path) -> PathBuf {
        let parent = path.parent().unwrap_or(Path::new("/"));
        let markers: &[&str] = match self {
            Self::Rust => &["Cargo.toml"],
            Self::Go => &["go.work", "go.mod"],
            Self::Markdown => &[".marksman.toml", ".git"],
        };
        parent
            .ancestors()
            .find(|dir| markers.iter().any(|name| dir.join(name).exists()))
            .unwrap_or(parent)
            .to_owned()
    }
}

#[derive(Clone, Debug, Deserialize)]
struct Position {
    line: usize,
    character: usize,
}
#[derive(Clone, Debug, Deserialize)]
struct Range {
    start: Position,
    end: Position,
}
#[derive(Clone, Debug, Deserialize)]
struct WireDiagnostic {
    range: Range,
    severity: Option<u8>,
    message: String,
}
#[derive(Debug, Deserialize)]
struct Publication {
    uri: String,
    version: Option<i32>,
    diagnostics: Vec<WireDiagnostic>,
}

#[derive(Clone, Debug)]
pub struct Diagnostic {
    pub start: (usize, usize),
    pub end: (usize, usize),
    pub severity: u8,
    pub message: String,
}
impl Diagnostic {
    fn covers_line(&self, row: usize) -> bool {
        row >= self.start.0
            && (row < self.end.0
                || (row == self.end.0 && (self.end.1 > 0 || self.start == self.end)))
    }
    fn contains(&self, row: usize, col: usize) -> bool {
        let pos = (row, col);
        pos >= self.start && (pos < self.end || (self.start == self.end && pos == self.start))
    }
}

#[derive(Clone)]
struct Document {
    uri: String,
    generation: u64,
    version: i32,
    saved: u64,
    save_event: bool,
    language: Language,
    text: String,
}
#[derive(PartialEq, Eq)]
struct Stamp {
    path: PathBuf,
    generation: u64,
    revision: u64,
    saved: u64,
}
enum Input {
    Document(Document),
    Stop,
}
enum Output {
    Ready,
    Diagnostics {
        generation: u64,
        version: i32,
        publication: Publication,
    },
    Failed(String),
    Tokens {
        document: TokenDocument,
        legend: crate::semantic::Legend,
        data: Option<Vec<u32>>,
    },
}
struct Session {
    input: Commands,
    output: Receiver<Output>,
    finished: Receiver<()>,
}
// Coalesce typing while retaining save and file-switch boundaries. A slow or
// initializing server must not accumulate one whole-buffer copy per keystroke.
struct Commands {
    queue: Arc<Mutex<VecDeque<Input>>>,
    wake: SyncSender<()>,
}
struct Inbox {
    queue: Arc<Mutex<VecDeque<Input>>>,
    wake: Receiver<()>,
}
fn mailbox() -> (Commands, Inbox) {
    let queue = Arc::new(Mutex::new(VecDeque::new()));
    let (tx, rx) = mpsc::sync_channel(1);
    (
        Commands {
            queue: queue.clone(),
            wake: tx,
        },
        Inbox { queue, wake: rx },
    )
}
impl Commands {
    fn try_send(&self, input: Input) -> io::Result<()> {
        let mut queue = self.queue.lock().unwrap();
        if let Input::Document(new) = &input
            && let Some(Input::Document(old)) = queue.back_mut()
            && old.uri == new.uri
            && old.generation == new.generation
            && old.saved == new.saved
            && !old.save_event
            && !new.save_event
        {
            *old = new.clone();
        } else {
            if queue.len() >= 32 && !matches!(input, Input::Stop) {
                return Err(io::Error::other("LSP command queue full"));
            }
            queue.push_back(input);
        }
        let _ = self.wake.try_send(());
        Ok(())
    }
}
impl Inbox {
    fn recv_timeout(&self, timeout: Duration) -> Result<Input, mpsc::RecvTimeoutError> {
        if let Some(input) = self.queue.lock().unwrap().pop_front() {
            return Ok(input);
        }
        self.wake.recv_timeout(timeout)?;
        self.queue
            .lock()
            .unwrap()
            .pop_front()
            .ok_or(mpsc::RecvTimeoutError::Timeout)
    }
}
impl Drop for Session {
    fn drop(&mut self) {
        let _ = self.input.try_send(Input::Stop);
    }
}

#[derive(Default)]
pub struct Client {
    enabled: bool,
    session: Option<Session>,
    retired: Vec<Session>,
    key: Option<(Language, PathBuf)>,
    stamp: Option<Stamp>,
    uri: String,
    version: i32,
    pub diagnostics: Vec<Diagnostic>,
    pub semantic_tokens: Vec<crate::semantic::Token>,
    pub semantic_revision: u64,
    pub status: String,
    failure: bool,
}
impl Client {
    pub fn enable(&mut self) {
        self.enabled = true;
    }
    pub fn restart(&mut self) {
        self.retire();
        self.key = None;
        self.stamp = None;
        self.diagnostics.clear();
        self.clear_tokens();
        self.status.clear();
        self.failure = false;
    }
    pub fn failure(&self) -> Option<&str> {
        self.failure.then_some(self.status.as_str())
    }
    pub fn counts(&self) -> (usize, usize) {
        (
            self.diagnostics.iter().filter(|d| d.severity == 1).count(),
            self.diagnostics.iter().filter(|d| d.severity == 2).count(),
        )
    }
    pub fn line_severity(&self, row: usize) -> Option<u8> {
        self.diagnostics
            .iter()
            .filter(|d| d.covers_line(row))
            .map(|d| d.severity)
            .min()
    }
    pub fn at_cursor(&self, row: usize, col: usize) -> Option<&str> {
        self.diagnostics
            .iter()
            .filter(|d| d.contains(row, col))
            .min_by_key(|d| d.severity)
            .or_else(|| {
                self.diagnostics
                    .iter()
                    .filter(|d| d.covers_line(row))
                    .min_by_key(|d| d.severity)
            })
            .map(|d| d.message.as_str())
    }
    fn fail(&mut self, message: String) {
        self.retire();
        self.diagnostics.clear();
        self.clear_tokens();
        self.status = message;
        self.failure = true;
    }
    pub fn sync(
        &mut self,
        path: &Path,
        generation: u64,
        revision: u64,
        saved: u64,
        text: impl FnOnce() -> String,
    ) {
        if !self.enabled {
            return;
        }
        let stamp = Stamp {
            path: path.to_owned(),
            generation,
            revision,
            saved,
        };
        if self.stamp.as_ref() == Some(&stamp) {
            return;
        }
        let identity_changed = self
            .stamp
            .as_ref()
            .is_none_or(|old| old.path != path || old.generation != generation);
        let contents_changed = identity_changed
            || self
                .stamp
                .as_ref()
                .is_none_or(|old| old.revision != revision);
        let save_event =
            !identity_changed && self.stamp.as_ref().is_some_and(|old| old.saved != saved);
        self.stamp = Some(stamp);
        if identity_changed {
            let Some(language) = Language::for_path(path) else {
                self.restart();
                self.stamp = Some(Stamp {
                    path: path.to_owned(),
                    generation,
                    revision,
                    saved,
                });
                return;
            };
            let absolute = match absolute_path(path) {
                Ok(path) => path,
                Err(err) => {
                    self.fail(format!("LSP path: {err}"));
                    return;
                }
            };
            self.uri = match Url::from_file_path(&absolute) {
                Ok(uri) => uri.to_string(),
                Err(()) => {
                    self.fail("LSP: invalid file path".into());
                    return;
                }
            };
            let root = language.root(&absolute);
            if self.key.as_ref() != Some(&(language, root.clone())) || self.failure {
                self.retire();
                let (program, args) = language.command();
                self.status = format!("{program}: starting");
                self.failure = false;
                self.session = Some(start_session(program, args, root.clone()));
                self.key = Some((language, root));
            }
        }
        if contents_changed {
            self.version = self.version.checked_add(1).unwrap_or(1);
            self.diagnostics.clear();
            self.clear_tokens();
        }
        if let (Some(session), Some((language, _))) = (&self.session, &self.key) {
            let document = Document {
                uri: self.uri.clone(),
                generation,
                version: self.version,
                saved,
                save_event,
                language: *language,
                text: text(),
            };
            if session.input.try_send(Input::Document(document)).is_err() {
                self.fail("LSP: server cannot keep up; :lsp-restart to retry".into());
            }
        }
    }
    pub fn poll(&mut self, lines: &[Vec<char>]) {
        self.retired
            .retain(|s| matches!(s.finished.try_recv(), Err(TryRecvError::Empty)));
        // Bound each UI iteration even if a server floods notifications.
        for _ in 0..64 {
            let event = match &self.session {
                Some(session) => session.output.try_recv(),
                None => break,
            };
            match event {
                Ok(Output::Tokens {
                    document,
                    legend,
                    data,
                }) => {
                    if self
                        .stamp
                        .as_ref()
                        .is_some_and(|s| s.generation == document.generation)
                        && self.uri == document.uri
                        && self.version == document.version
                    {
                        self.semantic_tokens = data
                            .and_then(|data| crate::semantic::decode(&data, &legend, lines))
                            .unwrap_or_default();
                        self.semantic_revision = self.semantic_revision.wrapping_add(1);
                    }
                }
                Ok(Output::Ready) => {
                    if let Some((language, _)) = &self.key {
                        self.status = language.command().0.into();
                    }
                }
                Ok(Output::Failed(message)) => {
                    self.fail(message);
                    break;
                }
                Ok(Output::Diagnostics {
                    generation,
                    version,
                    publication,
                }) => {
                    if self
                        .stamp
                        .as_ref()
                        .is_none_or(|s| s.generation != generation)
                        || version != self.version
                        || publication.uri != self.uri
                        || publication.version.is_some_and(|v| v != self.version)
                    {
                        continue;
                    }
                    self.diagnostics = publication
                        .diagnostics
                        .into_iter()
                        .filter_map(|d| {
                            let start = scalar_position(lines, &d.range.start)?;
                            let end = scalar_position(lines, &d.range.end)?;
                            (end >= start).then_some(Diagnostic {
                                start,
                                end,
                                severity: d.severity.filter(|s| (1..=4).contains(s)).unwrap_or(1),
                                message: d.message,
                            })
                        })
                        .collect();
                }
                Err(TryRecvError::Empty) => break,
                Err(TryRecvError::Disconnected) => {
                    self.fail("LSP: server stopped; :lsp-restart to retry".into());
                    break;
                }
            }
        }
    }
    fn retire(&mut self) {
        if let Some(session) = self.session.take() {
            let _ = session.input.try_send(Input::Stop);
            self.retired.push(session);
        }
    }
    fn clear_tokens(&mut self) {
        self.semantic_tokens.clear();
        self.semantic_revision = self.semantic_revision.wrapping_add(1);
    }
}
impl Drop for Client {
    fn drop(&mut self) {
        self.retire();
        // Keep shutdown workers alive long enough to close documents and reap
        // children before main exits. File switches never wait on this path.
        let deadline = Instant::now() + Duration::from_secs(1);
        for session in &self.retired {
            let _ = session
                .finished
                .recv_timeout(deadline.saturating_duration_since(Instant::now()));
        }
    }
}
fn absolute_path(path: &Path) -> io::Result<PathBuf> {
    let absolute = std::path::absolute(path)?;
    // New files do not exist yet, but their parent does. Canonicalization also
    // removes '..' and resolves symlinked directories before building a file URI.
    if absolute.exists() {
        absolute.canonicalize()
    } else {
        Ok(absolute
            .parent()
            .unwrap_or(Path::new("/"))
            .canonicalize()?
            .join(absolute.file_name().unwrap_or_default()))
    }
}
fn scalar_position(lines: &[Vec<char>], pos: &Position) -> Option<(usize, usize)> {
    let line = lines.get(pos.line)?;
    let length = line.len() - usize::from(pos.line + 1 < lines.len() && line.last() == Some(&'\r'));
    let mut units = 0;
    let mut col = 0;
    for c in line.iter().take(length) {
        if units + c.len_utf16() > pos.character {
            break;
        }
        units += c.len_utf16();
        col += 1;
    }
    Some((pos.line, col))
}
fn end_position(text: &str) -> Value {
    let line = text.bytes().filter(|b| *b == b'\n').count();
    let character = text
        .rsplit('\n')
        .next()
        .unwrap_or("")
        .encode_utf16()
        .count();
    json!({"line": line, "character": character})
}

// Bounded framing: Content-Length counts UTF-8 bytes, never scalar values.
fn read_message(reader: &mut impl BufRead) -> io::Result<Value> {
    let mut length = None;
    let mut header_bytes = 0;
    loop {
        let mut header = String::new();
        let read = reader.take(8193).read_line(&mut header)?;
        header_bytes += read;
        if read == 0 {
            return Err(io::Error::new(
                io::ErrorKind::UnexpectedEof,
                "server closed stdout",
            ));
        }
        if header_bytes > 8192 {
            return Err(io::Error::other("LSP header too large"));
        }
        if header == "\r\n" || header == "\n" {
            break;
        }
        let (name, value) = header
            .split_once(':')
            .ok_or_else(|| io::Error::other("invalid LSP header"))?;
        if name.eq_ignore_ascii_case("Content-Length") {
            if length.is_some() {
                return Err(io::Error::other("duplicate Content-Length"));
            }
            length = Some(value.trim().parse::<usize>().map_err(io::Error::other)?);
        }
    }
    let length = length
        .filter(|n| *n <= MAX_MESSAGE)
        .ok_or_else(|| io::Error::other("invalid LSP Content-Length"))?;
    let mut bytes = vec![0; length];
    reader.read_exact(&mut bytes)?;
    serde_json::from_slice(&bytes).map_err(io::Error::other)
}
fn write_message(writer: &mut impl Write, value: &Value) -> io::Result<()> {
    let bytes = serde_json::to_vec(value)?;
    write!(writer, "Content-Length: {}\r\n\r\n", bytes.len())?;
    writer.write_all(&bytes)?;
    writer.flush()
}
fn notify(method: &str, params: Value) -> Value {
    json!({"jsonrpc":"2.0", "method":method, "params":params})
}
fn send(sender: &SyncSender<Value>, value: Value) -> io::Result<()> {
    sender
        .try_send(value)
        .map_err(|_| io::Error::other("server pipe stalled"))
}

#[derive(Clone, Debug, PartialEq, Eq)]
struct TokenDocument {
    uri: String,
    generation: u64,
    version: i32,
}
impl From<&Document> for TokenDocument {
    fn from(d: &Document) -> Self {
        Self {
            uri: d.uri.clone(),
            generation: d.generation,
            version: d.version,
        }
    }
}
struct TokenRequest {
    id: u64,
    document: TokenDocument,
    started: Instant,
}
#[derive(Default)]
struct TokenRequests {
    legend: Option<crate::semantic::Legend>,
    pending: Option<TokenRequest>,
    document: Option<TokenDocument>,
    requested: bool,
    next_id: u64,
    retries: u8,
    retry_at: Option<Instant>,
}
impl TokenRequests {
    fn configure(&mut self, capabilities: &Value) {
        let provider = &capabilities["semanticTokensProvider"];
        if provider["full"] == true || provider["full"].is_object() {
            self.legend = serde_json::from_value(provider["legend"].clone()).ok();
        }
    }
    fn cancel(&mut self, tx: &SyncSender<Value>) -> io::Result<()> {
        if let Some(request) = self.pending.take() {
            send(tx, notify("$/cancelRequest", json!({"id":request.id})))?;
        }
        Ok(())
    }
    fn refresh(&mut self, tx: &SyncSender<Value>) -> io::Result<()> {
        self.cancel(tx)?;
        self.requested = false;
        self.retries = 0;
        self.retry_at = None;
        Ok(())
    }
    fn publish(&self, request: &TokenRequest, data: Option<Vec<u32>>, events: &SyncSender<Output>) {
        if let Some(legend) = &self.legend {
            let _ = events.try_send(Output::Tokens {
                document: request.document.clone(),
                legend: legend.clone(),
                data,
            });
        }
    }
    fn request(
        &mut self,
        document: &Document,
        tx: &SyncSender<Value>,
        events: &SyncSender<Output>,
    ) -> io::Result<()> {
        if self.legend.is_none() || !matches!(document.language, Language::Rust | Language::Go) {
            return Ok(());
        }
        let key = TokenDocument::from(document);
        if self.document.as_ref() != Some(&key) {
            self.refresh(tx)?;
            self.document = Some(key.clone());
        }
        if self
            .pending
            .as_ref()
            .is_some_and(|r| r.started.elapsed() >= TOKEN_TIMEOUT)
        {
            let request = self.pending.take().unwrap();
            send(tx, notify("$/cancelRequest", json!({"id":request.id})))?;
            self.publish(&request, None, events);
        }
        if self.pending.is_some()
            || self.requested
            || self.retry_at.is_some_and(|at| at > Instant::now())
        {
            return Ok(());
        }
        self.next_id = self.next_id.max(2) + 1;
        send(
            tx,
            json!({"jsonrpc":"2.0","id":self.next_id,"method":"textDocument/semanticTokens/full","params":{"textDocument":{"uri":document.uri}}}),
        )?;
        self.pending = Some(TokenRequest {
            id: self.next_id,
            document: key,
            started: Instant::now(),
        });
        self.requested = true;
        Ok(())
    }
    fn response(&mut self, value: &Value, events: &SyncSender<Output>) {
        if self
            .pending
            .as_ref()
            .is_none_or(|request| value["id"].as_u64() != Some(request.id))
        {
            return;
        }
        let request = self.pending.take().unwrap();
        #[derive(Deserialize)]
        struct Tokens {
            data: Vec<u32>,
        }
        let data = if value.get("error").is_some() {
            None
        } else if value["result"].is_null() {
            Some(vec![])
        } else {
            serde_json::from_value::<Tokens>(value["result"].clone())
                .ok()
                .map(|result| result.data)
        };
        self.publish(&request, data, events);
        if matches!(value["error"]["code"].as_i64(), Some(-32801 | -32802)) && self.retries < 2 {
            self.retries += 1;
            self.requested = false;
            self.retry_at = Some(Instant::now() + Duration::from_millis(250));
        }
    }
}

#[derive(Default)]
struct Protocol {
    desired: Option<Document>,
    sent: Option<Document>,
    ready: bool,
    change: u64,
    open_close: bool,
    save: Option<bool>,
    tokens: TokenRequests,
    go: bool,
}
impl Protocol {
    fn initialize(&mut self, result: &Value, tx: &SyncSender<Value>) -> io::Result<()> {
        let caps = &result["capabilities"];
        if caps["positionEncoding"]
            .as_str()
            .is_some_and(|s| s != "utf-16")
        {
            return Err(io::Error::other(
                "server selected an unsupported position encoding",
            ));
        }
        self.tokens.configure(caps);
        let sync = &caps["textDocumentSync"];
        if let Some(kind) = sync.as_u64() {
            self.change = kind;
            self.open_close = kind != 0;
        } else {
            self.change = sync["change"].as_u64().unwrap_or(0);
            self.open_close = sync["openClose"].as_bool().unwrap_or(false);
            self.save = match &sync["save"] {
                Value::Bool(true) => Some(false),
                Value::Object(_) => Some(sync["save"]["includeText"].as_bool().unwrap_or(false)),
                _ => None,
            };
        }
        self.ready = true;
        send(tx, notify("initialized", json!({})))?;
        self.flush(tx)
    }
    fn close(&mut self, tx: &SyncSender<Value>) -> io::Result<()> {
        self.tokens.refresh(tx)?;
        self.tokens.document = None;
        if let Some(old) = self.sent.take()
            && self.open_close
        {
            send(
                tx,
                notify(
                    "textDocument/didClose",
                    json!({"textDocument":{"uri":old.uri}}),
                ),
            )?;
        }
        Ok(())
    }
    fn flush(&mut self, tx: &SyncSender<Value>) -> io::Result<()> {
        if !self.ready {
            return Ok(());
        }
        let Some(new) = self.desired.take() else {
            return Ok(());
        };
        if self
            .sent
            .as_ref()
            .is_some_and(|old| old.uri != new.uri || old.generation != new.generation)
        {
            self.close(tx)?;
        }
        if let Some(old) = &self.sent {
            if old.version != new.version && self.change != 0 {
                let change = if self.change == 2 {
                    // A whole-document ranged replacement is valid incremental
                    // sync and avoids coupling the protocol to every edit command.
                    json!({"range":{"start":{"line":0,"character":0},"end":end_position(&old.text)},"text":new.text})
                } else {
                    json!({"text":new.text})
                };
                send(
                    tx,
                    notify(
                        "textDocument/didChange",
                        json!({"textDocument":{"uri":new.uri,"version":new.version},"contentChanges":[change]}),
                    ),
                )?;
            }
            if old.saved != new.saved
                && let Some(include_text) = self.save
            {
                let mut params = json!({"textDocument":{"uri":new.uri}});
                if include_text {
                    params["text"] = json!(new.text);
                }
                send(tx, notify("textDocument/didSave", params))?;
            }
        } else if self.open_close {
            send(
                tx,
                notify(
                    "textDocument/didOpen",
                    json!({"textDocument":{"uri":new.uri,"languageId":new.language.id(),"version":new.version,"text":new.text}}),
                ),
            )?;
        }
        self.sent = Some(new);
        Ok(())
    }
    fn reply(&mut self, value: &Value, root: &str, tx: &SyncSender<Value>) -> io::Result<()> {
        let id = &value["id"];
        let result = match value["method"].as_str().unwrap_or("") {
            "workspace/configuration" => json!(
                value["params"]["items"]
                    .as_array()
                    .map(|items| items
                        .iter()
                        .map(|item| match item["section"].as_str() {
                            Some("gopls") if self.go => json!({"semanticTokens":true}),
                            Some("gopls.semanticTokens") if self.go => json!(true),
                            _ => Value::Null,
                        })
                        .collect::<Vec<_>>())
                    .unwrap_or_default()
            ),
            "workspace/semanticTokens/refresh" => {
                self.tokens.refresh(tx)?;
                Value::Null
            }
            "workspace/workspaceFolders" => json!([{"uri":root,"name":"workspace"}]),
            "window/workDoneProgress/create" | "window/showMessageRequest" => Value::Null,
            "workspace/applyEdit" => {
                json!({"applied":false,"failureReason":"Coigüe does not apply workspace edits"})
            }
            _ => {
                return send(
                    tx,
                    json!({"jsonrpc":"2.0","id":id,"error":{"code":-32601,"message":"Unsupported client request"}}),
                );
            }
        };
        send(tx, json!({"jsonrpc":"2.0","id":id,"result":result}))
    }
}

struct Process(Child);
impl Drop for Process {
    fn drop(&mut self) {
        // Also reap after malformed output, timeouts, dropped UI, or unwinding.
        let _ = self.0.kill();
        let _ = self.0.wait();
    }
}
fn start_session(program: &str, args: &[&str], root: PathBuf) -> Session {
    let (input, commands) = mailbox();
    let (events, output) = mpsc::sync_channel(64);
    let (finished_tx, finished) = mpsc::channel();
    let program = program.to_owned();
    let args: Vec<String> = args.iter().map(|s| (*s).into()).collect();
    thread::spawn(move || {
        if let Err(err) = serve(&program, &args, &root, commands, &events, STARTUP_TIMEOUT) {
            let _ = events.try_send(Output::Failed(format!(
                "{program}: {err}; :lsp-restart to retry"
            )));
        }
        let _ = finished_tx.send(());
    });
    Session {
        input,
        output,
        finished,
    }
}
fn serve(
    program: &str,
    args: &[String],
    root: &Path,
    input: Inbox,
    events: &SyncSender<Output>,
    startup_timeout: Duration,
) -> io::Result<()> {
    let child = Command::new(program)
        .args(args)
        .current_dir(root)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .spawn()
        .map_err(|err| io::Error::other(format!("unavailable ({err})")))?;
    let mut child = Process(child);
    let mut stdin = child.0.stdin.take().unwrap();
    let stdout = child.0.stdout.take().unwrap();
    let (writer, outgoing) = mpsc::sync_channel::<Value>(64);
    let (incoming_tx, incoming) = mpsc::sync_channel(64);
    thread::spawn(move || {
        while let Ok(value) = outgoing.recv() {
            if write_message(&mut stdin, &value).is_err() {
                break;
            }
        }
    });
    thread::spawn(move || {
        let mut reader = BufReader::new(stdout);
        loop {
            let message = read_message(&mut reader);
            let failed = message.is_err();
            if incoming_tx.send(message).is_err() || failed {
                break;
            }
        }
    });
    let root_uri = Url::from_directory_path(root)
        .map_err(|_| io::Error::other("invalid workspace path"))?
        .to_string();
    send(
        &writer,
        json!({"jsonrpc":"2.0","id":1,"method":"initialize","params":{
            "processId":std::process::id(),"clientInfo":{"name":"coigue","version":env!("CARGO_PKG_VERSION")},
            "rootUri":root_uri,"workspaceFolders":[{"uri":root_uri,"name":root.file_name().unwrap_or_default().to_string_lossy()}],
            "initializationOptions": if program == "gopls" { json!({"semanticTokens":true}) } else { Value::Null },
            "capabilities":{"general":{"positionEncodings":["utf-16"]},
                "workspace":{"configuration":true,"workspaceFolders":true,"applyEdit":false,"semanticTokens":{"refreshSupport":true}},
                "textDocument":{"semanticTokens":{"dynamicRegistration":false,"requests":{"full":true},"tokenTypes":crate::semantic::TYPES,"tokenModifiers":crate::semantic::MODIFIERS,"formats":["relative"],"overlappingTokenSupport":false,"multilineTokenSupport":false,"serverCancelSupport":true,"augmentsSyntaxTokens":true},"synchronization":{"dynamicRegistration":false,"didSave":true},"publishDiagnostics":{"versionSupport":true}}}
        }}),
    )?;
    let started = Instant::now();
    let mut stopping = None;
    let mut protocol = Protocol {
        go: program == "gopls",
        ..Protocol::default()
    };
    let mut last_sync = Instant::now();
    loop {
        if let Some(status) = child.0.try_wait()? {
            return if stopping.is_some() {
                Ok(())
            } else {
                Err(io::Error::other(format!("server exited ({status})")))
            };
        }
        if stopping.is_some_and(|at: Instant| at.elapsed() >= SHUTDOWN_TIMEOUT) {
            return Ok(());
        }
        if !protocol.ready && started.elapsed() >= startup_timeout && stopping.is_none() {
            return Err(io::Error::other("initialization timed out"));
        }
        match input.recv_timeout(TICK) {
            Ok(Input::Document(document)) if stopping.is_none() => {
                let immediate = document.save_event
                    || protocol.sent.as_ref().is_none_or(|old| {
                        old.uri != document.uri || old.generation != document.generation
                    });
                protocol.desired = Some(document);
                if immediate {
                    protocol.flush(&writer)?;
                    last_sync = Instant::now();
                }
            }
            Ok(Input::Stop) | Err(mpsc::RecvTimeoutError::Disconnected) if stopping.is_none() => {
                stopping = Some(Instant::now());
                if protocol.ready {
                    protocol.flush(&writer)?;
                    protocol.close(&writer)?;
                    send(
                        &writer,
                        json!({"jsonrpc":"2.0","id":2,"method":"shutdown","params":null}),
                    )?;
                } else {
                    send(&writer, notify("exit", Value::Null))?;
                }
            }
            _ => {}
        }
        if stopping.is_none() && last_sync.elapsed() >= CHANGE_INTERVAL {
            protocol.flush(&writer)?;
            last_sync = Instant::now();
        }
        for _ in 0..64 {
            let value = match incoming.try_recv() {
                Ok(Ok(value)) => value,
                Ok(Err(err)) => return if stopping.is_some() { Ok(()) } else { Err(err) },
                Err(TryRecvError::Empty) => break,
                Err(TryRecvError::Disconnected) => {
                    return Err(io::Error::other("server pipe closed"));
                }
            };
            if value.get("method").is_some() {
                if value.get("id").is_some() {
                    protocol.reply(&value, &root_uri, &writer)?;
                } else if value["method"] == "textDocument/publishDiagnostics"
                    && stopping.is_none()
                    && let Ok(publication) =
                        serde_json::from_value::<Publication>(value["params"].clone())
                    && let Some(document) = &protocol.sent
                    && publication.uri == document.uri
                {
                    // Diagnostics are replaceable; avoid blocking the process
                    // supervisor if the editor is busy (e.g. saving to disk).
                    let _ = events.try_send(Output::Diagnostics {
                        generation: document.generation,
                        version: document.version,
                        publication,
                    });
                }
            } else if value["id"] == 1 && !protocol.ready && stopping.is_none() {
                if let Some(error) = value.get("error") {
                    return Err(io::Error::other(format!("initialize failed: {error}")));
                }
                if !value["result"]["capabilities"].is_object() {
                    return Err(io::Error::other("invalid initialize response"));
                }
                protocol.initialize(&value["result"], &writer)?;
                let _ = events.try_send(Output::Ready);
            } else if value["id"] == 2 && stopping.is_some() {
                send(&writer, notify("exit", Value::Null))?;
            } else if stopping.is_none() {
                protocol.tokens.response(&value, events);
            }
        }
        if stopping.is_none()
            && protocol.ready
            && protocol.desired.is_none()
            && let Some(document) = &protocol.sent
        {
            protocol.tokens.request(document, &writer, events)?;
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn semantic_capabilities() -> Value {
        json!({"semanticTokensProvider":{"full":{"delta":true},"legend":{"tokenTypes":["function"],"tokenModifiers":[]}}})
    }
    #[test]
    fn semantic_requests_negotiate_full_cancel_stale_and_refresh_without_reinitializing() {
        let (tx, rx) = mpsc::sync_channel(64);
        let (events, output) = mpsc::sync_channel(64);
        let mut tokens = TokenRequests::default();
        let first = doc("fn name() {}", 1);
        tokens.request(&first, &tx, &events).unwrap();
        assert!(rx.try_recv().is_err());
        tokens.configure(&semantic_capabilities());
        tokens.request(&first, &tx, &events).unwrap();
        let request = rx.recv().unwrap();
        assert_eq!(request["method"], "textDocument/semanticTokens/full");
        assert_eq!(request["id"], 3);
        tokens.request(&first, &tx, &events).unwrap();
        assert!(rx.try_recv().is_err());
        let second = doc("fn other() {}", 2);
        tokens.request(&second, &tx, &events).unwrap();
        assert_eq!(rx.recv().unwrap()["method"], "$/cancelRequest");
        assert_eq!(rx.recv().unwrap()["id"], 4);
        tokens.response(&json!({"id":3,"result":{"data":[0,3,4,0,0]}}), &events);
        assert!(output.try_recv().is_err());
        tokens.response(&json!({"id":4,"result":{"data":[0,3,5,0,0]}}), &events);
        let Output::Tokens { document, data, .. } = output.recv().unwrap() else {
            panic!()
        };
        assert_eq!(document.version, 2);
        assert_eq!(data.unwrap(), vec![0, 3, 5, 0, 0]);
        tokens.request(&second, &tx, &events).unwrap();
        assert!(rx.try_recv().is_err());
        tokens.refresh(&tx).unwrap();
        tokens.request(&second, &tx, &events).unwrap();
        assert_eq!(rx.recv().unwrap()["id"], 5);
        let mut markdown = doc("# title", 3);
        markdown.language = Language::Markdown;
        tokens.request(&markdown, &tx, &events).unwrap();
        assert!(rx.try_recv().is_err());
        let mut protocol = Protocol {
            go: true,
            ..Protocol::default()
        };
        protocol.reply(&json!({"id":9,"method":"workspace/configuration","params":{"items":[{"section":"gopls"},{"section":"gopls.semanticTokens"},{"section":"other"}]}}),"file:///tmp",&tx).unwrap();
        assert_eq!(
            rx.recv().unwrap()["result"],
            json!([{"semanticTokens":true},true,null])
        );
        protocol.tokens = tokens;
        protocol
            .reply(
                &json!({"id":"refresh","method":"workspace/semanticTokens/refresh"}),
                "file:///tmp",
                &tx,
            )
            .unwrap();
        assert_eq!(rx.recv().unwrap()["method"], "$/cancelRequest");
        assert_eq!(rx.recv().unwrap()["result"], Value::Null);
        assert!(!protocol.tokens.requested);
    }
    #[test]
    fn semantic_errors_retry_only_when_appropriate_and_timeouts_keep_diagnostics_alive() {
        let (tx, rx) = mpsc::sync_channel(64);
        let (events, output) = mpsc::sync_channel(64);
        let mut tokens = TokenRequests::default();
        tokens.configure(&semantic_capabilities());
        let document = doc("text", 1);
        for id in 3..=5 {
            tokens.retry_at = None;
            tokens.request(&document, &tx, &events).unwrap();
            assert_eq!(rx.recv().unwrap()["id"], id);
            tokens.response(
                &json!({"id":id,"error":{"code":-32801,"message":"modified"}}),
                &events,
            );
            assert!(matches!(
                output.recv().unwrap(),
                Output::Tokens { data: None, .. }
            ));
        }
        tokens.retry_at = None;
        tokens.request(&document, &tx, &events).unwrap();
        assert!(rx.try_recv().is_err()); // At most two retries per document/refresh.
        tokens.refresh(&tx).unwrap();
        tokens.request(&document, &tx, &events).unwrap();
        let id = rx.recv().unwrap()["id"].as_u64().unwrap();
        tokens.response(
            &json!({"id":id,"error":{"code":-32601,"message":"unsupported"}}),
            &events,
        );
        output.recv().unwrap();
        tokens.request(&document, &tx, &events).unwrap();
        assert!(rx.try_recv().is_err());
        tokens.refresh(&tx).unwrap();
        tokens.request(&document, &tx, &events).unwrap();
        rx.recv().unwrap();
        tokens.pending.as_mut().unwrap().started = Instant::now() - TOKEN_TIMEOUT;
        tokens.request(&document, &tx, &events).unwrap();
        assert_eq!(rx.recv().unwrap()["method"], "$/cancelRequest");
        assert!(matches!(
            output.recv().unwrap(),
            Output::Tokens { data: None, .. }
        ));
        tokens.request(&document, &tx, &events).unwrap();
        assert!(rx.try_recv().is_err());
    }

    fn doc(text: &str, version: i32) -> Document {
        Document {
            uri: "file:///tmp/test.rs".into(),
            generation: 0,
            version,
            saved: 0,
            save_event: false,
            language: Language::Rust,
            text: text.into(),
        }
    }
    fn publication(uri: &str, version: Option<i32>, message: &str) -> Publication {
        serde_json::from_value(json!({"uri":uri,"version":version,"diagnostics":[{
            "range":{"start":{"line":0,"character":1},"end":{"line":0,"character":3}},"severity":2,"message":message
        }]})).unwrap()
    }
    #[test]
    fn framing_handles_utf8_split_reads_and_rejects_bad_frames() {
        let value = json!({"text":"界😀\r\n"});
        let mut bytes = Vec::new();
        write_message(&mut bytes, &value).unwrap();
        write_message(&mut bytes, &Value::Null).unwrap();
        let mut reader = BufReader::with_capacity(1, bytes.as_slice());
        assert_eq!(read_message(&mut reader).unwrap(), value);
        assert_eq!(read_message(&mut reader).unwrap(), Value::Null);
        for data in [
            b"Content-Length: 999999999\r\n\r\n".as_slice(),
            b"Bad header\r\n\r\n",
            b"Content-Length: 10\r\n\r\n{}",
            b"Content-Length: 1\r\n\r\nx",
            b"Content-Length: 2\r\nContent-Length: 2\r\n\r\n{}",
            b"\r\n{}",
        ] {
            assert!(read_message(&mut BufReader::new(data)).is_err());
        }
        assert!(read_message(&mut BufReader::new(vec![b'x'; 9000].as_slice())).is_err());
    }
    #[test]
    fn utf16_positions_handle_emoji_combining_marks_crlf_and_eof() {
        let lines = crate::buffer::Buffer::new("a😀e\u{301}\r\n界\r\n").lines;
        for (units, col) in [(0, 0), (1, 1), (2, 1), (3, 2), (4, 3), (5, 4), (99, 4)] {
            assert_eq!(
                scalar_position(
                    &lines,
                    &Position {
                        line: 0,
                        character: units
                    }
                ),
                Some((0, col))
            );
        }
        assert_eq!(
            scalar_position(
                &lines,
                &Position {
                    line: 2,
                    character: 0
                }
            ),
            Some((2, 0))
        );
        assert!(
            scalar_position(
                &lines,
                &Position {
                    line: 3,
                    character: 0
                }
            )
            .is_none()
        );
        assert_eq!(end_position("a😀\r\n界😀"), json!({"line":1,"character":3}));
        assert_eq!(end_position("a😀\r\n"), json!({"line":1,"character":0}));
        assert_eq!(end_position(""), json!({"line":0,"character":0}));
    }
    #[test]
    fn languages_roots_and_new_file_uris() {
        let dir = tempfile::tempdir().unwrap();
        let sub = dir.path().join("sub space 界");
        std::fs::create_dir(&sub).unwrap();
        for marker in ["Cargo.toml", "go.mod", ".marksman.toml"] {
            std::fs::write(dir.path().join(marker), "").unwrap();
        }
        for (extension, language, command) in [
            ("rs", Language::Rust, "rust-analyzer"),
            ("go", Language::Go, "gopls"),
            ("md", Language::Markdown, "marksman"),
            ("markdown", Language::Markdown, "marksman"),
            ("MD", Language::Markdown, "marksman"),
        ] {
            let path = sub.join(format!("new #?界.{extension}"));
            assert_eq!(Language::for_path(&path), Some(language));
            assert_eq!(language.command().0, command);
            assert_eq!(language.root(&path), dir.path());
            let absolute = absolute_path(&path).unwrap();
            let uri = Url::from_file_path(&absolute).unwrap();
            assert!(uri.as_str().contains("%23%3F"));
            assert_eq!(uri.to_file_path().unwrap(), absolute);
        }
        assert_eq!(Language::for_path(Path::new("file.txt")), None);
        assert_eq!(Language::Go.root(&sub.join("a.go")), dir.path());
        std::fs::write(sub.join("go.work"), "").unwrap();
        assert_eq!(Language::Go.root(&sub.join("a.go")), sub);
    }
    #[test]
    fn typing_coalesces_but_preserves_saves_switches_and_stop() {
        let (commands, inbox) = mailbox();
        for version in 1..10000 {
            commands
                .try_send(Input::Document(doc("typing", version)))
                .unwrap();
        }
        assert_eq!(commands.queue.lock().unwrap().len(), 1);
        let mut saved = doc("saved", 10000);
        saved.saved = 1;
        saved.save_event = true;
        commands.try_send(Input::Document(saved)).unwrap();
        let mut after_save = doc("unsaved again", 10001);
        after_save.saved = 1;
        commands.try_send(Input::Document(after_save)).unwrap();
        let mut next = doc("next", 10002);
        next.generation = 1;
        commands.try_send(Input::Document(next)).unwrap();
        commands.try_send(Input::Stop).unwrap();
        for (version, saved) in [(9999, 0), (10000, 1), (10001, 1), (10002, 0)] {
            let Input::Document(document) = inbox.recv_timeout(TICK).unwrap() else {
                panic!()
            };
            assert_eq!((document.version, document.saved), (version, saved));
        }
        assert!(matches!(inbox.recv_timeout(TICK), Ok(Input::Stop)));
    }
    #[test]
    fn synchronization_negotiates_full_incremental_and_disabled_changes() {
        for kind in [0, 1, 2] {
            let (tx, rx) = mpsc::sync_channel(64);
            let mut p = Protocol {
                desired: Some(doc("a😀\r\n界", 1)),
                ..Protocol::default()
            };
            p.flush(&tx).unwrap();
            assert!(rx.try_recv().is_err()); // No notification before initialize response.
            p.initialize(&json!({"capabilities":{"textDocumentSync":{"openClose":true,"change":kind,"save":{"includeText":true}}}}), &tx).unwrap();
            assert_eq!(rx.recv().unwrap()["method"], "initialized");
            assert_eq!(
                rx.recv().unwrap()["params"]["textDocument"]["text"],
                "a😀\r\n界"
            );
            let mut new = doc("changed", 2);
            new.saved = 1;
            p.desired = Some(new);
            p.flush(&tx).unwrap();
            if kind != 0 {
                let change = rx.recv().unwrap();
                assert_eq!(change["method"], "textDocument/didChange");
                assert_eq!(change["params"]["textDocument"]["version"], 2);
                assert_eq!(change["params"]["contentChanges"][0]["text"], "changed");
                if kind == 2 {
                    assert_eq!(
                        change["params"]["contentChanges"][0]["range"]["end"],
                        json!({"line":1,"character":1})
                    );
                } else {
                    assert!(change["params"]["contentChanges"][0].get("range").is_none());
                }
            }
            let save = rx.recv().unwrap();
            assert_eq!(save["method"], "textDocument/didSave");
            assert_eq!(save["params"]["text"], "changed");
            let mut next = doc("next", 3);
            next.uri = "file:///tmp/renamed.rs".into();
            next.generation = 1;
            p.desired = Some(next);
            p.flush(&tx).unwrap();
            assert_eq!(rx.recv().unwrap()["method"], "textDocument/didClose");
            assert_eq!(
                rx.recv().unwrap()["params"]["textDocument"]["uri"],
                "file:///tmp/renamed.rs"
            );
            p.close(&tx).unwrap();
            assert_eq!(rx.recv().unwrap()["method"], "textDocument/didClose");
            assert!(rx.try_recv().is_err());
        }
        let (tx, rx) = mpsc::sync_channel(64);
        let mut p = Protocol::default();
        assert!(
            p.initialize(&json!({"capabilities":{"positionEncoding":"utf-8"}}), &tx)
                .is_err()
        );
        p.initialize(&json!({"capabilities":{"textDocumentSync":2}}), &tx)
            .unwrap();
        assert_eq!(p.change, 2);
        assert!(p.open_close);
        assert_eq!(p.save, None);
        rx.recv().unwrap();
        p.reply(
            &json!({"id":"config","method":"workspace/configuration","params":{"items":[{},{}]}}),
            "file:///tmp",
            &tx,
        )
        .unwrap();
        assert_eq!(rx.recv().unwrap()["result"], json!([null, null]));
        p.reply(&json!({"id":9,"method":"unknown"}), "file:///tmp", &tx)
            .unwrap();
        assert_eq!(rx.recv().unwrap()["error"]["code"], -32601);
    }
    #[test]
    fn diagnostics_reject_stale_versions_generations_and_other_files_and_clear_on_edits() {
        let dir = tempfile::tempdir().unwrap();
        std::fs::write(dir.path().join("Cargo.toml"), "").unwrap();
        let root = dir.path().canonicalize().unwrap();
        let path = root.join("test.rs");
        let uri = Url::from_file_path(&path).unwrap().to_string();
        let (input, _inbox) = mailbox();
        let (events, output) = mpsc::sync_channel(64);
        let (_finished_tx, finished) = mpsc::channel();
        let mut client = Client::default();
        client.enable();
        client.session = Some(Session {
            input,
            output,
            finished,
        });
        client.key = Some((Language::Rust, root));
        let buffer = crate::buffer::Buffer::new("a😀b");
        client.sync(&path, 0, 0, 0, || buffer.text());
        for (generation, stamp, version, other_uri) in [
            (1, 1, Some(1), false),
            (0, 0, None, false),
            (0, 1, Some(0), false),
            (0, 1, Some(1), true),
        ] {
            events
                .send(Output::Diagnostics {
                    generation,
                    version: stamp,
                    publication: publication(
                        if other_uri { "file:///other" } else { &uri },
                        version,
                        "stale",
                    ),
                })
                .unwrap();
        }
        client.poll(&buffer.lines);
        assert!(client.diagnostics.is_empty());
        events
            .send(Output::Diagnostics {
                generation: 0,
                version: 1,
                publication: publication(&uri, Some(1), "warning 😀"),
            })
            .unwrap();
        client.poll(&buffer.lines);
        assert_eq!(client.counts(), (0, 1));
        assert_eq!(client.diagnostics[0].end, (0, 2));
        assert_eq!(client.at_cursor(0, 1), Some("warning 😀"));
        client.sync(&path, 0, 1, 0, || "updated".into());
        assert!(client.diagnostics.is_empty());
        events
            .send(Output::Diagnostics {
                generation: 0,
                version: 2,
                publication: publication(&uri, None, "unversioned"),
            })
            .unwrap();
        client.poll(&buffer.lines);
        assert_eq!(client.diagnostics.len(), 1);
        events
            .send(Output::Diagnostics {
                generation: 0,
                version: 2,
                publication: Publication {
                    uri: uri.clone(),
                    version: Some(2),
                    diagnostics: vec![],
                },
            })
            .unwrap();
        client.poll(&buffer.lines);
        assert!(client.diagnostics.is_empty());
        let legend: crate::semantic::Legend = serde_json::from_value(
            semantic_capabilities()["semanticTokensProvider"]["legend"].clone(),
        )
        .unwrap();
        for (generation, version, token_uri) in [
            (1, 2, uri.clone()),
            (0, 1, uri.clone()),
            (0, 2, "file:///other.rs".into()),
        ] {
            events
                .send(Output::Tokens {
                    document: TokenDocument {
                        uri: token_uri,
                        generation,
                        version,
                    },
                    legend: legend.clone(),
                    data: Some(vec![0, 1, 2, 0, 0]),
                })
                .unwrap();
        }
        client.poll(&buffer.lines);
        assert!(client.semantic_tokens.is_empty());
        events
            .send(Output::Tokens {
                document: TokenDocument {
                    uri: uri.clone(),
                    generation: 0,
                    version: 2,
                },
                legend: legend.clone(),
                data: Some(vec![0, 1, 2, 0, 0]),
            })
            .unwrap();
        client.poll(&buffer.lines);
        assert_eq!(client.semantic_tokens[0].start, 1);
        assert_eq!(client.semantic_tokens[0].end, 2);
        let cache_revision = client.semantic_revision;
        client.sync(&path, 0, 2, 0, || "edited again".into());
        assert!(client.semantic_tokens.is_empty());
        assert!(client.semantic_revision > cache_revision);
        events
            .send(Output::Tokens {
                document: TokenDocument {
                    uri: uri.clone(),
                    generation: 0,
                    version: 2,
                },
                legend: legend.clone(),
                data: Some(vec![0, 1, 2, 0, 0]),
            })
            .unwrap();
        client.poll(&buffer.lines);
        assert!(client.semantic_tokens.is_empty());
        events
            .send(Output::Tokens {
                document: TokenDocument {
                    uri,
                    generation: 0,
                    version: 3,
                },
                legend,
                data: Some(vec![0, 1, 2, 0, 0]),
            })
            .unwrap();
        client.poll(&buffer.lines);
        assert_eq!(client.semantic_tokens.len(), 1);
        events
            .send(Output::Failed("server crashed".into()))
            .unwrap();
        client.poll(&buffer.lines);
        assert!(client.semantic_tokens.is_empty());
        assert_eq!(client.failure(), Some("server crashed"));
        // Do not wait on a synthetic session with no worker.
        client.session = None;
        client.retired.clear();
    }

    const FAKE_SERVER: &str = r#"
import json, os, sys, time
mode, log = sys.argv[1:]
def send(value):
    data = json.dumps(value, ensure_ascii=False).encode()
    sys.stdout.buffer.write(('Content-Length: %d\r\n\r\n' % len(data)).encode() + data)
    sys.stdout.buffer.flush()
while True:
    headers = {}
    while True:
        line = sys.stdin.buffer.readline()
        if not line: sys.exit(0)
        if line == b'\r\n': break
        k, v = line.decode().split(':', 1)
        headers[k.lower()] = v.strip()
    msg = json.loads(sys.stdin.buffer.read(int(headers['content-length'])))
    with open(log, 'a') as f: f.write(json.dumps(msg) + '\n')
    method = msg.get('method')
    if method == 'initialize':
        if mode == 'hang': time.sleep(30)
        if mode == 'reject': send({'jsonrpc':'2.0','id':1,'error':{'code':-1,'message':'rejected'}}); continue
        if mode == 'malformed': sys.stdout.buffer.write(b'bad header\r\n\r\n'); sys.stdout.buffer.flush(); continue
        send({'jsonrpc':'2.0','id':1,'result':{'capabilities':{'textDocumentSync':{'openClose':True,'change':2,'save':True}}}})
        send({'jsonrpc':'2.0','id':'configuration','method':'workspace/configuration','params':{'items':[{'section':'gopls'}]}})
    elif method in ('textDocument/didOpen','textDocument/didChange'):
        doc = msg['params']['textDocument']
        send({'jsonrpc':'2.0','method':'textDocument/publishDiagnostics','params':{'uri':doc['uri'],'version':doc['version'],'diagnostics':[{'range':{'start':{'line':0,'character':0},'end':{'line':0,'character':1}},'severity':1,'message':'fake error'}]}})
        if mode == 'crash': os._exit(7)
    elif method == 'shutdown':
        if mode == 'stuck': time.sleep(30)
        send({'jsonrpc':'2.0','id':msg['id'],'result':None})
    elif method == 'exit': sys.exit(0)
"#;
    fn fake(
        mode: &str,
        timeout: Duration,
    ) -> (
        tempfile::TempDir,
        Commands,
        Receiver<Output>,
        Receiver<io::Result<()>>,
    ) {
        let dir = tempfile::tempdir().unwrap();
        let script = dir.path().join("fake.py");
        std::fs::write(&script, FAKE_SERVER).unwrap();
        let args = vec![
            "-u".into(),
            script.to_str().unwrap().into(),
            mode.into(),
            dir.path().join("log").to_str().unwrap().into(),
        ];
        let (commands, inbox) = mailbox();
        let (tx, rx) = mpsc::sync_channel(64);
        let (done_tx, done) = mpsc::channel();
        let root = dir.path().to_owned();
        thread::spawn(move || {
            let _ = done_tx.send(serve("python3", &args, &root, inbox, &tx, timeout));
        });
        (dir, commands, rx, done)
    }
    fn receive_diagnostic(events: &Receiver<Output>, version: i32) {
        loop {
            match events.recv_timeout(Duration::from_secs(3)).unwrap() {
                Output::Diagnostics { publication, .. } if publication.version == Some(version) => {
                    assert_eq!(publication.diagnostics[0].message, "fake error");
                    break;
                }
                _ => {}
            }
        }
    }
    #[test]
    fn fake_process_roundtrip_saves_switches_and_graceful_shutdown() {
        let (dir, commands, events, done) = fake("normal", Duration::from_secs(2));
        commands
            .try_send(Input::Document(doc("a😀\r\n", 1)))
            .unwrap();
        receive_diagnostic(&events, 1);
        let mut saved = doc("new", 2);
        saved.saved = 1;
        commands.try_send(Input::Document(saved)).unwrap();
        receive_diagnostic(&events, 2);
        let mut next = doc("next", 3);
        next.generation = 1;
        commands.try_send(Input::Document(next)).unwrap();
        receive_diagnostic(&events, 3);
        commands.try_send(Input::Stop).unwrap();
        done.recv_timeout(Duration::from_secs(3)).unwrap().unwrap();
        let log: Vec<Value> = std::fs::read_to_string(dir.path().join("log"))
            .unwrap()
            .lines()
            .map(|line| serde_json::from_str(line).unwrap())
            .collect();
        let methods: Vec<_> = log.iter().filter_map(|v| v["method"].as_str()).collect();
        assert_eq!(
            methods,
            [
                "initialize",
                "initialized",
                "textDocument/didOpen",
                "textDocument/didChange",
                "textDocument/didSave",
                "textDocument/didClose",
                "textDocument/didOpen",
                "textDocument/didClose",
                "shutdown",
                "exit"
            ]
        );
        assert!(
            log.iter()
                .any(|v| v["id"] == "configuration" && v["result"] == json!([null]))
        );
    }
    #[test]
    fn missing_crashed_rejected_malformed_and_stalled_servers_are_bounded() {
        let dir = tempfile::tempdir().unwrap();
        let (_commands, inbox) = mailbox();
        let (tx, _rx) = mpsc::sync_channel(64);
        let err = serve(
            dir.path().join("absent").to_str().unwrap(),
            &[],
            dir.path(),
            inbox,
            &tx,
            Duration::from_secs(1),
        )
        .unwrap_err();
        assert!(err.to_string().contains("unavailable"));
        for (mode, expected) in [
            ("crash", ""),
            ("reject", "initialize failed"),
            ("malformed", "invalid LSP header"),
            ("hang", "timed out"),
        ] {
            let (_dir, commands, _events, done) = fake(mode, Duration::from_millis(300));
            commands.try_send(Input::Document(doc("a", 1))).unwrap();
            let err = done
                .recv_timeout(Duration::from_secs(3))
                .unwrap()
                .unwrap_err();
            assert!(err.to_string().contains(expected), "{mode}: {err}");
        }
        let (_dir, commands, events, done) = fake("stuck", Duration::from_secs(2));
        commands.try_send(Input::Document(doc("a", 1))).unwrap();
        receive_diagnostic(&events, 1);
        commands.try_send(Input::Stop).unwrap();
        done.recv_timeout(Duration::from_secs(2)).unwrap().unwrap();
    }
}
