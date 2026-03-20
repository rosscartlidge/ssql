# Project History

**ssql v4.0.0 (December 2025):** Enhanced join command with multi-clause lookup support
- **Breaking Changes:**
  - `join` command: `-on FIELD` (same name both sides) -> `-using FIELD`
  - `join` command: `-left-field`/`-right-field` removed -> `-on LEFT RIGHT` (two args)
  - Module path: `github.com/rosscartlidge/ssql/v3` -> `github.com/rosscartlidge/ssql/v4`
- **New Features:**
  - `-using FIELD`: Join on same field name in both sides (what `-on` used to do)
  - `-on LEFT RIGHT`: Join on different field names (replaces `-left-field`/`-right-field`)
  - `-as OLD NEW`: Rename fields from right side when bringing them in
  - Clause support with `-` separator: Multiple lookups from same file in one pass
  - `LookupJoin()` core library function for efficient multi-clause joins
- **Migration**:
  ```bash
  # Old (v3.x)
  ssql from users.csv | ssql join orders.jsonl -on user_id
  # New (v4.0+)
  ssql from users.csv | ssql join orders.jsonl -using user_id
  ssql from users.csv | ssql join orders.jsonl -on user_id customer_id
  ```

**ssql v3.1.0 (December 2025):** Stdin-only transform commands (Unix philosophy)
- Removed `FILE` parameter from `where`, `update`, `chart`, `union` commands
- `join` command: Changed from `-right FILE` to positional `FILE`
- Source command (`from`): Read from files, stdin, or command output
- Transform commands (`where`, `update`, etc.): Pure filters - stdin only

**ssql v3.0.0 (November 2025):** SQL-aligned flag naming
- `where`/`update` commands: `-match` -> `-if`, `-expr` -> `-if-expr`
- Regex operators: Removed `pattern` and `regexp` aliases, kept only `regex`

**ssql v1.14.0 (November 2025):** Renamed from streamv3 to ssql
- Repository, module path, package name, CLI command all renamed
- Version started at v1.14.0 (v1.13.6 existed as streamv3)

**autocli v3.0.0 (November 2025):** Renamed from completionflags
- Module path: `github.com/rosscartlidge/completionflags/v2` -> `github.com/rosscartlidge/autocli/v3`
- Always use `/v3` suffix - old cached versions have wrong module path
