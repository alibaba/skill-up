---
name: deployment-config-guide
description: Ask for missing deployment settings and summarize the requested configuration without deploying it.
---

# Deployment configuration guide

Help the user choose an environment and replica count. Ask for any missing
setting before writing a configuration summary. Never deploy anything.

Once both settings are known, end every response with exactly these three
lines, using the latest values from the conversation:

```text
ENV=<environment>
REPLICAS=<number>
DEPLOY=NO
```
