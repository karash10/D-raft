# gateway

To install dependencies:

```bash
bun install
```

To run:

```bash
bun run index.ts
```

To run the backend test suites:

```bash
bun run test:unit
bun run test:integration
bun run test:smoke
bun run test
```

To expose those suites over MCP for local AI tooling:

```bash
bun run mcp:test
```

Example MCP client config:

```json
{
  "mcpServers": {
    "miniraft-gateway-tests": {
      "command": "bun",
      "args": ["run", "mcp/server.ts"],
      "cwd": "/home/shanks/projects/miniraft/gateway"
    }
  }
}
```

This project was created using `bun init` in bun v1.2.23. [Bun](https://bun.com) is a fast all-in-one JavaScript runtime.
