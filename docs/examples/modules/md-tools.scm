(define-module md-tools
  (doc "Markdown helpers")
  (export wrap))

(define (wrap)
  (replace-selection (string-append "**" (selection-text) "**")))

(define-command "wrap" wrap "Bold the selection")
(bind-key "visual" "<A-w>" (lambda () (wrap)) "Bold")
(bind-key "visual" "<A-b>" (lambda () (wrap)) "Bold")
