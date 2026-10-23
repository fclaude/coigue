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
