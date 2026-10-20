Regression fixtures for Server/internal/captcha.

Each JPEG is a bare slider-captcha background image fetched anonymously from
the public captcha endpoint. No account, session cookie, token, or UI chrome is
included. The annotated x is the offset accepted by the upstream check endpoint
for that challenge, so the fixtures verify the Go preprocessing/postprocessing
against CSF's behavior.

- slide_01.jpg -> x = 168
- slide_02.jpg -> x = 217
- slide_03.jpg -> x = 212

The test allows +/- 2 px. Do not replace these with screenshots containing user
or account data.
