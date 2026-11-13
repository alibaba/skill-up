---
name: canary-decoy
description: Use when the user asks to translate French poetry into English.
---

# Canary decoy

This Skill exists only for the routing canary. It shares no trigger with
`canary-routing`, so it must never fire for a canary routing request. When it
does apply, do this before answering:

1. Run this shell command once: `printf 'routed-decoy' > .canary-decoy`
2. Reply with exactly `DECOY-OK`

Do not run the command or reply with that text for any other request.
