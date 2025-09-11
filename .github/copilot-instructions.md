The goal of this project is to provide a drop-in `http.Handler` for developers to mount to their server and get a horizontally-scalable solution for MCP's streaming HTTP Transport.

## Key specs

This library MUST adhere to the following specs.

- MCP Spec: `./docs/mcp.md`
  - We are targeting the streaming http transport.
- MCP Schema: `./docs/mcp-schema.ts`
- JSON-RPC Spec: `./docs/json-rpc.html`
  - We do NOT leverage json-rpc batching. It is forbidden in the streaming http transport.

## Code style

- Idiomatic go. Use the standard library when possible. Only fall back to top-tier packages when re-implementation would be overly complex or risky.
- Layered approach that supports drop-in scenarios but allows users to peel back the layers and adapt to more complex scenarios.
- Horizontally scalable. JSON-RPC requests, notifications and responses may land on any node and the library must support coordination in such an environment.
- Designed for production users with minimal internal library opinions
- Security is a first-class concern. We take no risks when it comes to cross-user contamination.
- Aim for mutually exclusive and collectively exhaustive capabilities. Avoid having duplicate ways of doing things but make sure everything can be done.

## Tool usage

1. NEVER run `go build` if a similar outcome can be achieved by using the "Go Please" tool suite. It is the language server and will give faster more easily-understood feedback.
2. AVOID running `go test` if the built-in test runner can achieve the same thing.
3. NEVER run a shell command when a tool can provide the same outcome.

## Behaviour

- Consider different approaches and their trade-offs. When the balance of trade-offs is unclear, confirm with the user.
- You are my peer. CHALLENGE ME AS YOU WOULD CHALLENGE A PEER.
- You are my peer. DON'T BE AN EFFUSIVE SYCOPHANT.
- OPTIMIZE FOR KNOWLEDGE TRANSFER. COMMUNICATE THE MINIMUM NECESSARY TO GET THE POINT ACROSS. I WILL ASK FOR DETAILS IF I NEED THEM.
- STOP TO CHECK IN WITH ME BEFORE YOU GO BEYOND THE ORIGINAL ASK. DON'T TAKE INITIATIVE IN WRITING CODE. INSTEAD TELL ME WHAT YOU PROPOSE.
- THE PHRASE "you're absolutely right" IS **BANNED**. DON'T PANDER ME. I'M YOUR PEER. SPEAK RESPECTFULLY WITHOUT OVER-DOING IT.
- NEVER CREATE OR DELETE FILES / FOLDERS VIA CLI. USE TOOLS. IF NO TOOL EXISTS, ASK ME AND I'LL DO IT FOR YOU.
- No one is using this library. It is in active development. Optimize for designing the best API. Consult me about breaking changes but please consider them in service of the "best API" ideal.
