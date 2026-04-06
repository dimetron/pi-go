# Requirements

## Questions & Answers

**Q1: Purpose of web-serve?**
A: Simple web UI to expose terminal with pi-go

**Q2: Terminal interaction style?**
A: Full terminal emulator (xterm.js style)

**Q3: Terminal backend?**
A: Run pi-go in project folder via PTY

**Q4: Authentication?**
A: Pair code via QR code

**Q5: Pair code flow?**
A: Server generates code+QR → user scans/enters in pi-go mobile → pi-go approves → browser gains access

**Q6: Web framework?**
A: Standard library `net/http`

**Q7: WebSocket library?**
A: Standard `github.com/gorilla/websocket`

**Q8: Port?**
A: 8080 (default)

**Q9: Session model?**
A: A single web application may have multiple sessions as tabs. Each session is a combination of a project and an actual running agent session.

**Q10: How is project specified?**
A: Path on filesystem

## Summary

1. **Purpose**: Web UI exposing terminal with pi-go agent
2. **Terminal**: Full xterm.js emulator in browser
3. **Backend**: Run pi-go in project folder via PTY
4. **Auth**: Pair code + QR; mobile app approves
5. **Flow**: Generate code+QR → mobile approves → browser terminal access
6. **Framework**: `net/http` (standard library)
7. **WebSocket**: `github.com/gorilla/websocket`
8. **Port**: 8080
9. **Sessions**: Each browser tab = session = (project path + agent session)
