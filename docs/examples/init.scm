(define (wrap open close)
  (replace-selection (string-append open (selection-text) close)))

(define-command "wrap" wrap
  "Wrap the selection in OPEN and CLOSE, for example :wrap ( ) or :wrap ** **")
