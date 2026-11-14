---
name: valid-tricky-skill
description: Valid skill whose SKILL.md uses markdown constructs that must not fool reference extraction.
---

# Valid Tricky Skill

Used by the validate --strict tests: the eval config is valid and every local
citation exists on disk, so even --strict must pass despite the tricky
markdown below.

See references/guide.md. It is also linked as [the guide](references/guide.md#setup)
and cited inline as `references/guide.md`.

The icon comes from a remote URL, not a local attachment:
[icon](https://example.com/assets/icon.png)

~~~sh
# Example command in a tilde-fenced block — not a reference.
cat scripts/example.sh
~~~
