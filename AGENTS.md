# Development notes

- Read `README.md` and `docs/data-model.md` before changing sync or output contracts.
- Run `go test -race ./...`, `go vet ./...`, and `python3 scripts/check-secrets.py` before pushing.
- Tests must use synthetic fixtures and `httptest`; never require real API keys or export real reading data into this repository.
- Keep gateway wire types in `internal/weread`, stable records in `internal/domain`, and orchestration in `internal/service`.
- Never infer successful deletion from missing arrays or incomplete pagination. Sync commits only after the entire requested fetch succeeds. Preserve data on failure.
- Offline commands must not initialize the network client or require authentication.
- Do not log credentials, request Authorization headers, complete error bodies, or personal note content during sync. Credentials and SQLite state belong outside the repository.
- `next.command`, stable note IDs, JSON/JSONL fields and inclusive date filters are scripting contracts.
- This is a public repository. Never commit shell profiles, auth files, personal exports, database files, local logs, or compiled binaries.
