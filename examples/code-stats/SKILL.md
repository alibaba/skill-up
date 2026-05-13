---
name: code-stats
description: Analyzes code files and reports statistics including line counts, file counts by extension, and total size. Use this skill whenever the user wants to understand the composition of a codebase - asking about "how many lines of code", "what file types exist", "code distribution", or needing a quick audit of project size and structure. Make sure to invoke this skill when users mention analyzing codebases, counting lines, checking file distributions, or auditing code.
version: 1.0.0
---

# Code Stats Skill

Analyzes code files and generates statistics about a codebase.

## Usage

When invoked, this skill will:
1. Scan the specified directory (defaults to current directory)
2. Count total lines, files by extension
3. Report the largest files
4. Output results in a structured format
5. Sort extension summaries by total line count in descending order

## Output Format

Always use this exact format for the output:

```
# Code Statistics

## Summary
- Total Files: <count>
- Total Lines: <count>
- Total Size: <size_bytes> bytes

## Files by Extension
| Extension | Files | Lines |
|-----------|-------|-------|
| .go | 12 | 1500 |
| .md | 5 | 200 |

## Top 5 File Extensions by Line Count
1. .go — 1500 lines
2. .md — 200 lines

## Largest Files (top 5)
1. main.go (500 lines)
2. util.go (300 lines)
```

If a file has no extension, group it under `(no ext)` in both extension sections.

## Process

1. Use `Bash` with `find` to locate all code files
2. Use `Bash` with `wc -l` to count lines per file
3. Group files with no extension under `(no ext)`
4. Sort both extension summaries by total line count in descending order
5. Aggregate the results into the output format above
6. If no directory is specified, analyze the current working directory

## Examples

- "Analyze the current directory"
- "Run code-stats on ./src"
- "Show me file type distribution"
- "How many lines of Python do we have?"
