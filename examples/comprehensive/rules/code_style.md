---
description: Go code style conventions — naming, formatting, and idiomatic patterns.
alwaysApply: false
---

## Go Code Style

1. Use `camelCase` for unexported identifiers, `PascalCase` for exported ones.
2. Keep functions short — under 40 lines when possible.
3. Return errors as the last return value; never panic in library code.
4. Use `context.Context` as the first parameter for functions that do I/O.
5. Prefer table-driven tests.
6. Use `errors.Is` / `errors.As` for error inspection, not string matching.
