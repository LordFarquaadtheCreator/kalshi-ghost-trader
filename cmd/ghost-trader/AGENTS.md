# cmd/ghost-trader

Entrypoint. Wires all components via errgroup.

## Wiring order

1. Load config
2. Load RSA signer
3. Open SQLite
4. Create tick writer
5. Create REST client
6. Create WS manager
7. Create tracker
8. Create scanner
9. Create scheduler
10. Launch goroutines via errgroup

## Shutdown

SIGINT/SIGTERM cancels root ctx. errgroup cancels all. Then:
- `tr.StopAll()` — unsubscribes all tracked markets
- `db.Close()` — closes SQLite (after tick writer flushed)

## Gotchas

- Don't move `db.Close()` before errgroup `Wait()`. Tick writer may still flush.
- Don't add goroutines outside errgroup. Won't get cancelled on signal.
