; Open log files at their last line, where the news is.
(define (ends-with? text suffix)
  (let ((n (string-length text)) (m (string-length suffix)))
    (and (>= n m) (string=? (substring text (- n m) n) suffix))))

(on-start
 (lambda ()
   (if (ends-with? (buffer-path) ".log")
       (begin
         (set-cursor (line-count) 1)
         (message "Log file: at the last line")))))
