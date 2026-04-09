# Requirements

## Questions & Answers

### Q1: What is the context for this A2A client?
**A:** Implementability to call other agents via A2A protocol. User should configure the endpoint for A2A in the config file settings, and then will be able to call this sub-agent via A2A.

### Q2: What A2A protocol spec does this client need to implement?
**A:** https://github.com/a2aproject/a2a-go - The official A2A Go SDK (github.com/a2aproject/a2a-go/v2)

### Q3: How should users configure the A2A sub-agent endpoint?
**A:** Via config.json file (consistent with existing pi-go config at `~/.pi-go/config.json` and `.pi-go/config.json`)

### Q4: What should happen after the user calls the A2A sub-agent?
**A:** Both streaming and non-streaming responses supported

### Q5: What "ADK RemoteA2A Agent" pattern to follow?
**A:** As a tool - the A2A client should be exposed as a callable tool

### Q6: Should the A2A tool support streaming responses?
**A:** Both streaming and non-streaming options

### Q7: How should the config.json structure look?
**A:**
```json
{
  "A2A": {
    "agents": [
      { "name": "helper", "url": "http://localhost:8080" },
      { "name": "coder", "url": "http://localhost:8081" }
    ]
  }
}
```

### Q8: What inputs should the A2A tool accept?
**A:**
- `agent_name` - which configured agent to call
- `prompt` - the message to send
- `stream` - optional boolean to control streaming behavior

## Summary

Implement an A2A client that:
1. Uses the `github.com/a2aproject/a2a-go/v2` SDK
2. Reads agent configuration from `.pi-go/config.json`
3. Registers A2A agents as callable tools in pi-go
4. Supports both streaming and non-streaming response modes
5. Tool accepts: agent_name, prompt, and optional stream parameter
