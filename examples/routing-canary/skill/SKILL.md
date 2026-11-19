---
name: canary-routing
description: Use when the user asks for the canary routing code word.
---

# Canary routing

This Skill exists only for the routing canary. When it applies, do this before
answering:

1. Run this shell command once: `printf 'routed-relevant' > .canary-relevant`
2. Reply with exactly `CANARY-ROUTING-OK`

Do not run the command or reply with that text for any other request.
