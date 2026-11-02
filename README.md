<p align="center">
  <img src="docs/images/botnet-banner.png" alt="Botnet — console for hosts you administer" width="100%" />
</p>

# Botnet

> **Under development.** This project is not a finished release. Behavior, the console layout, and the agent protocol can still change.

Botnet is a small educational console for machines you own or are allowed to administer. A desktop app enrolls agents, watches their health, opens a remote shell on those hosts, and can send paced traffic to a target you control.

It is not a Python script. The console is a Go and Wails desktop app, and the agent is a Go program built by that console.

**[What it does](#what-it-does) · [Layout](#layout) · [Limits](#limits)**

## What it does

- **Remote monitoring.** Each enrolled agent reports hostname, operating system, CPU, memory, uptime, and a measured download speed. The console keeps a registry of who is online, which version they run, and roughly where their public IP places them. That location is a network lookup, not GPS.
- **Remote management.** From the console you build an agent with this console’s address embedded, ship a signed update, and revoke a client. Settings for retry, heartbeat, and how many clients may enroll live with the console.
- **Remote shell.** The client inspector can open a shell on an online agent over the agent’s existing TCP connection. Use it only on hosts you administer.
- **Paced traffic.** A load run tells online agents to send traffic, for a limited time and rate, to a host and port you choose. Point it only at servers you administer.

*The banner is concept artwork, not a screenshot of the app.*

## Layout

| Path | Contents |
| --- | --- |
| `main.go` | Desktop console |
| `frontend/` | Console interface |
| `cmd/agent` | Monitoring agent |
| `cmd/build-agent` | Builds an agent with console addresses embedded |
| `internal/` | Console, agent, updates, and metrics |
| `tests/` | Browser checks |
| `build/bin/` | Local `botnet` and `botnet-agent` binaries |

**Stack:** Go, Wails v2, HTML, CSS, JavaScript, and Playwright.

## Limits

> Run this only with permission. This page is an overview. It is not a guide to deploying agents or directing traffic.

- The agent speaks raw TCP. The application does not encrypt that traffic. Do not expose the listener to a network you do not trust.
- Signed updates check that a release was signed. They do not hide the connection.
- Console state can hold host details and agent secrets. Keep state files, the update signing key, and identifying screenshots out of public repositories.
- Location lookup and bandwidth checks call `ipwho.is` and `speed.cloudflare.com`.
- A passing UI test is not a claim about how many real hosts the console can run.
