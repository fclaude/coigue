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
