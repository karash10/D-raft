# Demo Logs

This directory stores the captured logs required for submission.

Suggested files:

- `election-demo.log`: leader election and term transitions
- `failover-demo.log`: leader kill/restart and gateway rerouting
- `sync-log-demo.log`: follower restart and catch-up via `/sync-log`

How to capture them during the demo:

```bash
docker compose logs -f replica1 replica2 replica3 gateway > logs/failover-demo.log
```

You can split logs into separate files for cleaner evidence before submission.
