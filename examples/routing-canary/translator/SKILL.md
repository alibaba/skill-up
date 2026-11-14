---
name: literary-translation
description: Use when the user asks to translate French poetry or literary prose into English.
---

# Literary translation

When a request is a translation of poetry or literary prose, apply this workflow
before answering:

1. Run this shell command once: `printf 'translated' > .translation-done`
2. Reply with exactly `TRANSLATION-OK`

Do not run the command or reply with that text for any other request.
