; A starting init.scm. Copy it to ~/.config/coigue/init.scm (or
; $XDG_CONFIG_HOME/coigue/init.scm). :reload reads it again.

(define (wrap open close)
  (replace-selection (string-append open (selection-text) close)))

(define-command "wrap" wrap
  "Wrap the selection in OPEN and CLOSE, for example :wrap ( ) or :wrap ** **")

; Ctrl-D copies the current line below itself and keeps the cursor where it
; was, one line down.
(define (duplicate-line)
  (let* ((line (cursor-line))
         (column (cursor-column))
         (text (buffer-text line)))
    (insert-at line (+ (string-length text) 1) (string-append "\n" text))
    (set-cursor (+ line 1) column)))

(bind-key "normal" "<C-d>" duplicate-line "Copy the current line below it")

; Alt-J and Alt-K swap the current line with the one below or above it. The
; line keeps its text, so the same column is still a column of it.
(define (move-line offset)
  (let ((line (cursor-line))
        (other (+ (cursor-line) offset)))
    (if (and (>= other 1) (<= other (line-count)))
        (let ((first (min line other))
              (column (cursor-column)))
          (replace-lines first (+ first 1)
                         (string-append (buffer-text (+ first 1)) "\n" (buffer-text first)))
          (set-cursor other column)))))

(bind-key "normal" "<A-j>" (lambda () (move-line 1)) "Move the current line down")
(bind-key "normal" "<A-k>" (lambda () (move-line -1)) "Move the current line up")

; U in Visual mode shouts the selection. u is undo in Normal mode and does
; nothing in Visual mode, so a capital keeps the pair easy to remember.
(bind-key "visual" "U"
          (lambda () (replace-selection (string-upcase (selection-text))))
          "Make the selection upper case")

; J joins the next line onto this one, with a space between them, and
; keeps the cursor where it was. The last line is left as it is.
(define (join-next)
  (let ((line (cursor-line)))
    (if (< line (line-count))
        (let ((column (cursor-column))
              (here (buffer-text line))
              (below (buffer-text (+ line 1))))
          (replace-lines line (+ line 1) (string-append here " " below))
          (set-cursor line column)))))

(bind-key "normal" "J" join-next "Join the next line onto this one")

; Alt-; puts a comment marker at the start of the current line, or
; removes it when the line already starts with that marker, space
; included. A .scm or .el file uses "; ", a .py, .sh or .rb file uses
; "# ", and anything else uses "// ". The cursor stays on the same
; character when the line still has it.
(define (ends-with? text suffix)
  (let ((n (string-length text)) (m (string-length suffix)))
    (and (>= n m) (string=? (substring text (- n m) n) suffix))))

(define (starts-with? text prefix)
  (let ((n (string-length text)) (m (string-length prefix)))
    (and (>= n m) (string=? (substring text 0 m) prefix))))

(define (comment-prefix)
  (let ((path (buffer-path)))
    (if (or (ends-with? path ".scm") (ends-with? path ".el"))
        "; "
        (if (or (ends-with? path ".py")
                (ends-with? path ".sh")
                (ends-with? path ".rb"))
            "# "
            "// "))))

(define (toggle-comment)
  (let* ((line (cursor-line))
         (column (cursor-column))
         (text (buffer-text line))
         (prefix (comment-prefix))
         (n (string-length prefix)))
    (if (starts-with? text prefix)
        (let ((at (- column n)))
          (replace-lines line line (substring text n (string-length text)))
          (set-cursor line (if (>= at 1) at 1)))
        (begin
          (insert-at line 1 prefix)
          (set-cursor line (+ column n))))))

(bind-key "normal" "<A-;>" toggle-comment
          "Comment the current line, or remove the comment")

; A theme called dusk. It starts from Gruvbox and changes two colours
; and the weight of keywords.
(define-theme "dusk"
  '(("base" . "gruvbox")
    ("keyword" . "#ff79c6")
    ("string" . "#8be9fd")
    ("bold-keywords" . #t)))

; Start the editor with it. :theme mocha (or any other theme) still switches
; while you're editing, and nothing is saved, so the next start is dusk again.
(set-theme "dusk")

; Open log files at their last line, where the news is.
(on-start
 (lambda ()
   (if (ends-with? (buffer-path) ".log")
       (begin
         (set-cursor (line-count) 1)
         (message "Log file: at the last line")))))
