(define-module text-tools
  (doc "Wrap and reshape the selection")
  (export wrap))

(define (surround open close)
  (replace-selection (string-append open (selection-text) close)))

(define (wrap open close)
  (surround open close))

(define-command "wrap" wrap "Wrap the selection in OPEN and CLOSE")
(bind-key "visual" "<A-w>" (lambda () (wrap "(" ")")) "Parenthesize")
