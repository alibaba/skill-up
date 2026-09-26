---
name: codeblock-refs
description: Reference-looking paths appear only inside fenced code blocks.
---

# Code Block References

A typical skill layout looks like this:

```text
my-skill/
├── SKILL.md
├── references/
│   └── imagined.md
└── scripts/
    └── placeholder.sh
```

And a usage example would be:

```bash
cat references/imagined.md
sh scripts/placeholder.sh
```

None of these paths exist on disk; they are documentation examples and must
not be reported.
