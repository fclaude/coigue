(require text-tools)
(require md-tools (prefix md:))

(define (bold)
  (md:wrap)
  (message "bolded"))

(bind-key "visual" "<A-w>" bold "Bold the selection")
