# Checks by outcome

| Change | Recipe |
| --- | --- |
| Agent Keys: assignment, order, hosts, `agent.show`, `press`, Rex following | [Agent Keys reach the right pane](agent-keys.md) |
| Bindings that post shortcuts, layers by `match`, front-app handling | [Shortcuts fire with Rex in front](shortcuts.md) |

`go test ./...` covers the engine and daemon against a fake Rex and pad; it
does not show that the real Rex app moved. Lighting, the touch sensor and
hardware keycodes still need the pad (`rex-micro test`) and are not covered here.
