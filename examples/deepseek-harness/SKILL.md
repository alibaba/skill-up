---
name: dsh-marker-reader
description: Decode the marker fixture used by the DeepSeek Harness integration example.
---

# DSH marker reader

When asked to decode `marker.txt`:

1. Read the file from the current workspace.
2. Reverse its complete non-empty line.
3. Respond with exactly `DSH_SKILL_UP_OK=<decoded-value>` and no other text.
