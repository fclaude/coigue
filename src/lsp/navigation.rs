//! LSP 3.17 definition, references, document-symbol and workspace-symbol requests.
//! https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/
use super::*;
use crate::navigation::{Location, Target};
use std::collections::HashMap;

pub const TIMEOUT: Duration = Duration::from_secs(5);
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Kind {
    Definition,
    References,
    Symbols,
    Workspace,
}
impl Kind {
    pub fn title(self) -> &'static str {
        match self {
            Self::Definition => "Definitions",
            Self::References => "References",
            Self::Symbols => "File symbols",
            Self::Workspace => "Workspace symbols",
        }
    }
    fn method(self) -> &'static str {
        match self {
            Self::Definition => "textDocument/definition",
            Self::References => "textDocument/references",
            Self::Symbols => "textDocument/documentSymbol",
            Self::Workspace => "workspace/symbol",
        }
    }
    fn capability(self) -> &'static str {
        match self {
            Self::Definition => "definitionProvider",
            Self::References => "referencesProvider",
            Self::Symbols => "documentSymbolProvider",
            Self::Workspace => "workspaceSymbolProvider",
        }
    }
}
#[derive(Clone)]
pub(super) struct Request {
    pub serial: u64,
    pub kind: Kind,
    pub query: String,
    pub document: TokenDocument,
    pub row: usize,
    pub character: usize,
}
impl Request {
    fn id(&self) -> String {
        format!("navigation-{}", self.serial)
    }
}
struct Pending {
    request: Request,
    text: String,
    started: Instant,
}
#[derive(Default)]
pub(super) struct Requests {
    pub capabilities: Value,
    queued: Option<Request>,
    pending: Option<Pending>,
}
impl Requests {
    pub fn cancel(&mut self, tx: &SyncSender<Value>) -> io::Result<()> {
        self.queued = None;
        if let Some(old) = self.pending.take() {
            send(
                tx,
                notify("$/cancelRequest", json!({"id":old.request.id()})),
            )?;
        }
        Ok(())
    }
    pub fn queue(&mut self, request: Request, tx: &SyncSender<Value>) -> io::Result<()> {
        self.cancel(tx)?;
        self.queued = Some(request);
        Ok(())
    }
    fn publish(
        request: &Request,
        result: Result<Vec<Target>, String>,
        events: &SyncSender<Output>,
    ) {
        let _ = events.try_send(Output::Navigation {
            request: request.clone(),
            result,
        });
        // The UI also owns a deadline, so a saturated event channel cannot
        // leave an interactive request waiting indefinitely.
    }
    pub fn tick(
        &mut self,
        document: Option<&Document>,
        tx: &SyncSender<Value>,
        events: &SyncSender<Output>,
    ) -> io::Result<()> {
        if self
            .pending
            .as_ref()
            .is_some_and(|p| p.started.elapsed() >= TIMEOUT)
        {
            let pending = self.pending.take().unwrap();
            send(
                tx,
                notify("$/cancelRequest", json!({"id":pending.request.id()})),
            )?;
            Self::publish(&pending.request, Err("Language-server navigation timed out; editing and project search remain available".into()), events);
        }
        let Some(document) = document else {
            return Ok(());
        };
        let Some(request) = self.queued.take() else {
            return Ok(());
        };
        if request.document != TokenDocument::from(document) {
            return Ok(());
        }
        let supported = &self.capabilities[request.kind.capability()];
        if supported != true && !supported.is_object() {
            Self::publish(
                &request,
                Err(format!(
                    "The language server does not support {}; use project search instead",
                    request.kind.title().to_lowercase()
                )),
                events,
            );
            return Ok(());
        }
        let mut params = json!({"textDocument":{"uri":request.document.uri}});
        match request.kind {
            Kind::Definition | Kind::References => {
                params["position"] = json!({"line":request.row,"character":request.character});
                if request.kind == Kind::References {
                    params["context"] = json!({"includeDeclaration":true});
                }
            }
            Kind::Workspace => params = json!({"query":request.query}),
            Kind::Symbols => {}
        }
        send(
            tx,
            json!({"jsonrpc":"2.0","id":request.id(),"method":request.kind.method(),"params":params}),
        )?;
        self.pending = Some(Pending {
            request,
            text: document.text.clone(),
            started: Instant::now(),
        });
        Ok(())
    }
    pub fn response(&mut self, value: &Value, events: &SyncSender<Output>) {
        if self
            .pending
            .as_ref()
            .is_none_or(|p| value["id"].as_str() != Some(p.request.id().as_str()))
        {
            return;
        }
        let pending = self.pending.take().unwrap();
        let result = if let Some(error) = value.get("error") {
            Err(format!(
                "Language-server navigation failed: {}",
                error["message"].as_str().unwrap_or("request rejected")
            ))
        } else if let Some(result) = value.get("result") {
            parse_targets(result, &pending.request.document.uri, &pending.text)
        } else {
            Err("Invalid language-server navigation response".into())
        };
        Self::publish(&pending.request, result, events);
    }
}

// Normalize Location, LocationLink, flat SymbolInformation and hierarchical
// DocumentSymbol responses to scalar positions. This runs on the worker, with
// the exact unsaved source text attached to the request, never on the UI thread.
fn parse_targets(
    result: &Value,
    current_uri: &str,
    current_text: &str,
) -> Result<Vec<Target>, String> {
    if result.is_null() {
        return Ok(Vec::new());
    }
    let values = if let Some(values) = result.as_array() {
        values.clone()
    } else if result.is_object() {
        vec![result.clone()]
    } else {
        return Err("Invalid navigation result".into());
    };
    let mut queue: VecDeque<_> = values
        .into_iter()
        .map(|value| (value, String::new(), 0))
        .collect();
    let mut texts = HashMap::<String, Option<Vec<Vec<char>>>>::new();
    texts.insert(
        current_uri.into(),
        Some(
            current_text
                .split('\n')
                .map(|l| l.chars().collect())
                .collect(),
        ),
    );
    let mut targets = Vec::new();
    let mut rejected = 0;
    let mut visited = 0;
    while let Some((value, parent, depth)) = queue.pop_front() {
        visited += 1;
        if targets.len() == 1000 || visited > 10000 {
            break;
        }
        let name = value["name"].as_str().unwrap_or("");
        let title = if parent.is_empty() {
            name.to_owned()
        } else {
            format!("{parent} / {name}")
        };
        if depth < 32
            && let Some(children) = value["children"].as_array()
        {
            queue.extend(
                children
                    .iter()
                    .take(1000)
                    .cloned()
                    .map(|child| (child, title.clone(), depth + 1)),
            );
        }
        let location = value.get("location").unwrap_or(&value);
        let uri = if let Some(uri) = location["uri"]
            .as_str()
            .or_else(|| location["targetUri"].as_str())
        {
            uri
        } else if !name.is_empty() && location.get("selectionRange").is_some() {
            current_uri
        } else {
            rejected += 1;
            continue;
        };
        let start = location
            .get("targetSelectionRange")
            .or_else(|| location.get("selectionRange"))
            .or_else(|| location.get("range"))
            .and_then(|r| r.get("start"));
        let Some(start) = start else {
            rejected += 1;
            continue;
        };
        let Ok(position) = serde_json::from_value::<Position>(start.clone()) else {
            rejected += 1;
            continue;
        };
        let Some(path) = Url::parse(uri).ok().and_then(|u| u.to_file_path().ok()) else {
            rejected += 1;
            continue;
        };
        let lines = texts.entry(uri.into()).or_insert_with(|| {
            let file = std::fs::File::open(&path).ok()?;
            let metadata = file.metadata().ok()?;
            if !metadata.is_file() || metadata.len() > 2 * 1024 * 1024 {
                return None;
            }
            let mut text = String::new();
            file.take(2 * 1024 * 1024 + 1)
                .read_to_string(&mut text)
                .ok()?;
            (text.len() <= 2 * 1024 * 1024)
                .then(|| text.split('\n').map(|l| l.chars().collect()).collect())
        });
        let Some((row, col)) = lines
            .as_ref()
            .and_then(|lines| scalar_position(lines, &position))
        else {
            rejected += 1;
            continue;
        };
        let mut location = Location::new(path);
        location.row = row;
        location.col = col;
        if targets
            .iter()
            .any(|t: &Target| t.location == location && t.label.starts_with(&title))
        {
            continue;
        }
        let label = format!(
            "{}{}:{}:{}",
            if title.is_empty() {
                String::new()
            } else {
                format!("{title}  ")
            },
            location.path.display(),
            row + 1,
            col + 1
        );
        targets.push(Target { location, label });
    }
    if targets.is_empty() && rejected > 0 {
        Err("No usable local-file locations in the server response".into())
    } else {
        Ok(targets)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    fn range(line: usize, character: usize) -> Value {
        json!({"start":{"line":line,"character":character},"end":{"line":line,"character":character+1}})
    }
    fn doc() -> Document {
        Document {
            uri: "file:///tmp/coigue-navigation.rs".into(),
            generation: 7,
            version: 2,
            saved: 0,
            save_event: false,
            language: Language::Rust,
            text: "a😀target\nfn local() {}".into(),
        }
    }
    fn request(kind: Kind, serial: u64) -> Request {
        Request {
            serial,
            kind,
            query: "Target".into(),
            document: TokenDocument::from(&doc()),
            row: 0,
            character: 3,
        }
    }
    #[test]
    fn parses_links_flat_and_nested_symbols_with_unsaved_utf16_text() {
        let doc = doc();
        let links = json!([{"targetUri":doc.uri,"targetRange":range(0,0),"targetSelectionRange":range(0,3)}]);
        let hits = parse_targets(&links, &doc.uri, &doc.text).unwrap();
        assert_eq!((hits[0].location.row, hits[0].location.col), (0, 2));
        let symbols = json!([{"name":"Outer","selectionRange":range(0,3),"children":[{"name":"local","selectionRange":range(1,3)}]}]);
        let hits = parse_targets(&symbols, &doc.uri, &doc.text).unwrap();
        assert_eq!(hits.len(), 2);
        assert!(hits[1].label.contains("Outer / local"));
        assert_eq!((hits[1].location.row, hits[1].location.col), (1, 3));
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("space café #界.rs");
        std::fs::write(&path, "😀 target").unwrap();
        let flat = json!([{"name":"Target","location":{"uri":Url::from_file_path(&path).unwrap().to_string(),"range":range(0,3)}}]);
        assert_eq!(
            parse_targets(&flat, &doc.uri, &doc.text).unwrap()[0]
                .location
                .col,
            2
        );
        assert!(
            parse_targets(
                &json!([{"uri":"https://invalid.example/","range":range(0,0)}]),
                &doc.uri,
                &doc.text
            )
            .is_err()
        );
        assert!(parse_targets(&json!([{"range":range(0,0)}]), &doc.uri, &doc.text).is_err());
        assert!(
            parse_targets(&json!(null), &doc.uri, &doc.text)
                .unwrap()
                .is_empty()
        );
    }
    #[test]
    fn requests_negotiate_capabilities_cancel_superseded_work_and_time_out() {
        let (tx, rx) = mpsc::sync_channel(16);
        let (events, output) = mpsc::sync_channel(16);
        let mut requests = Requests::default();
        requests.queue(request(Kind::Definition, 1), &tx).unwrap();
        requests.tick(Some(&doc()), &tx, &events).unwrap();
        assert!(matches!(
            output.try_recv().unwrap(),
            Output::Navigation { result: Err(_), .. }
        ));
        assert!(rx.try_recv().is_err());
        requests.capabilities = json!({"definitionProvider":{},"referencesProvider":true,"documentSymbolProvider":true,"workspaceSymbolProvider":true});
        requests.queue(request(Kind::Definition, 2), &tx).unwrap();
        requests.tick(Some(&doc()), &tx, &events).unwrap();
        let value = rx.try_recv().unwrap();
        assert_eq!(value["id"], "navigation-2");
        assert_eq!(value["params"]["position"]["character"], 3);
        requests.queue(request(Kind::References, 3), &tx).unwrap();
        assert_eq!(rx.try_recv().unwrap()["method"], "$/cancelRequest");
        requests.response(&json!({"id":"navigation-2","result":null}), &events);
        assert!(output.try_recv().is_err());
        requests.tick(Some(&doc()), &tx, &events).unwrap();
        assert_eq!(
            rx.try_recv().unwrap()["params"]["context"]["includeDeclaration"],
            true
        );
        requests.pending.as_mut().unwrap().started = Instant::now() - TIMEOUT;
        requests.tick(Some(&doc()), &tx, &events).unwrap();
        assert!(matches!(
            output.try_recv().unwrap(),
            Output::Navigation { result: Err(_), .. }
        ));
        assert_eq!(rx.try_recv().unwrap()["method"], "$/cancelRequest");
        requests.queue(request(Kind::Workspace, 4), &tx).unwrap();
        requests.tick(Some(&doc()), &tx, &events).unwrap();
        assert_eq!(rx.try_recv().unwrap()["params"]["query"], "Target");
        requests.response(
            &json!({"id":"navigation-4","error":{"code":-32603,"message":"fixture failure"}}),
            &events,
        );
        assert!(
            matches!(output.try_recv().unwrap(),Output::Navigation { result:Err(message), .. } if message.contains("fixture failure"))
        );
    }
    #[test]
    fn client_rejects_stale_replies_and_converts_outgoing_unicode_position() {
        let document = doc();
        let (input, inbox) = mailbox();
        let (events, output) = mpsc::sync_channel(16);
        let (_done, finished) = mpsc::channel();
        let mut client = Client::default();
        client.session = Some(Session {
            input,
            output,
            finished,
        });
        client.stamp = Some(Stamp {
            path: "/tmp/coigue-navigation.rs".into(),
            generation: 7,
            revision: 1,
            saved: 0,
        });
        client.uri = document.uri.clone();
        client.version = 2;
        let lines = document
            .text
            .split('\n')
            .map(|l| l.chars().collect())
            .collect::<Vec<_>>();
        client
            .navigate(Kind::Definition, String::new(), 0, 2, &lines)
            .unwrap();
        let Input::Navigate(sent) = inbox.recv_timeout(Duration::from_secs(1)).unwrap() else {
            panic!("missing request");
        };
        assert_eq!(sent.character, 3);
        for variant in 0..4 {
            let mut stale = sent.clone();
            match variant {
                0 => stale.document.version = 1,
                1 => stale.document.generation += 1,
                2 => stale.document.uri.push_str(".other"),
                _ => stale.serial += 1,
            }
            events
                .send(Output::Navigation {
                    request: stale,
                    result: Ok(Vec::new()),
                })
                .unwrap();
            client.poll(&lines);
            assert!(client.navigation_result.is_none());
        }
        events
            .send(Output::Navigation {
                request: sent,
                result: Ok(Vec::new()),
            })
            .unwrap();
        client.poll(&lines);
        assert!(matches!(
            client.navigation_result.take(),
            Some((Kind::Definition, Ok(_)))
        ));
        client
            .navigate(Kind::Symbols, String::new(), 0, 0, &lines)
            .unwrap();
        client.navigation_pending.as_mut().unwrap().1 =
            Instant::now() - TIMEOUT - Duration::from_secs(2);
        client.poll(&lines);
        assert!(matches!(
            client.navigation_result.take(),
            Some((Kind::Symbols, Err(_)))
        ));
    }
}
