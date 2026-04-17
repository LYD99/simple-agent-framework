---
description: Core safety constraints — dangerous command blocklist and permission boundaries.
alwaysApply: true
---

## Safety Rules

1. Never execute destructive commands such as `rm -rf /`, `DROP DATABASE`, or `FORMAT`.
2. Do not access or expose secrets, credentials, or private keys.
3. Always confirm with the user before performing irreversible operations.
4. If a tool call fails with a permission error, stop and report — do not retry with elevated privileges.
