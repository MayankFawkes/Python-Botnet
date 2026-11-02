import net from "node:net";
import { test, expect } from "@playwright/test";

const b64 = (text) => Buffer.from(text, "utf8").toString("base64");

// fakeAgent speaks the agent wire protocol so tests can drive the Shell tab
// without a real client process.
function fakeAgent() {
  const socket = net.connect(34117, "127.0.0.1");
  const inbox = [];
  const waiters = [];
  let buf = "";
  const dispatch = (msg) => {
    const index = waiters.findIndex((w) => w.op === msg.op);
    if (index >= 0) {
      const [waiter] = waiters.splice(index, 1);
      clearTimeout(waiter.timer);
      waiter.resolve(msg);
    } else {
      inbox.push(msg);
    }
  };
  socket.on("data", (chunk) => {
    buf += chunk.toString();
    let index;
    while ((index = buf.indexOf("\n")) >= 0) {
      try {
        dispatch(JSON.parse(buf.slice(0, index)));
      } catch {
        // ignore malformed lines
      }
      buf = buf.slice(index + 1);
    }
  });
  return {
    send: (msg) => socket.write(JSON.stringify(msg) + "\n"),
    next(op, timeoutMs = 10000) {
      const queued = inbox.findIndex((m) => m.op === op);
      if (queued >= 0) return Promise.resolve(inbox.splice(queued, 1)[0]);
      return new Promise((resolve, reject) => {
        const timer = setTimeout(
          () => reject(new Error(`timed out waiting for ${op}`)),
          timeoutMs,
        );
        waiters.push({ op, resolve, timer });
      });
    },
    close: () => socket.end(),
  };
}

async function enroll(agent, hostname) {
  agent.send({
    op: "enroll",
    hostname,
    os: "linux",
    arch: "amd64",
    version: "2.0.0",
  });
  await agent.next("enrolled");
  agent.send({
    op: "beat",
    cpuPercent: 1,
    memUsed: 100,
    memTotal: 8000,
    uptimeSec: 5,
  });
  await agent.next("ok");
}

// The terminal renders on canvas, so assertions read the emulator's buffer
// instead of the DOM.
function bufferText(page) {
  return page.evaluate(() => {
    const term = window.__botnetShellTerm;
    if (!term) return "";
    const buffer = term.buffer.active;
    let out = "";
    for (let i = 0; i < buffer.length; i++) {
      out += (buffer.getLine(i)?.translateToString(true) ?? "") + "\n";
    }
    return out;
  });
}

test.beforeEach(async ({ request }) => {
  const response = await request.post("/api/Reset", { data: {} });
  expect(response.ok()).toBe(true);
});

test("shell tab renders PTY output in a terminal emulator", async ({
  page,
}) => {
  const agent = fakeAgent();
  await enroll(agent, "shell-node");

  await page.goto("/");
  await page
    .getByRole("button", { name: "Inspect shell-node", exact: true })
    .click();
  await page.locator('[data-detail-tab="shell"]').click();

  // fish-style startup: capability queries followed by a colored prompt.
  await agent.next("shell-open");
  agent.send({
    op: "shell-out",
    shellData: b64("\x1b[?u\x1b[0c\x1b[32mnode@lab\x1b[0m:~$ "),
  });
  await expect.poll(() => bufferText(page)).toContain("node@lab:~$");
  expect(await bufferText(page)).not.toContain("[?u");

  // Progress-style redraw keeps only the final line state.
  agent.send({ op: "shell-out", shellData: b64("\r\nWorking: 10%") });
  await expect.poll(() => bufferText(page)).toContain("Working: 10%");
  agent.send({ op: "shell-out", shellData: b64("\r\x1b[2KWorking: 100%") });
  await expect.poll(() => bufferText(page)).toContain("Working: 100%");
  expect(await bufferText(page)).not.toContain("Working: 10%");

  // Typed input goes straight into the terminal and reaches the agent.
  await page.locator("#terminal-output").click();
  await page.keyboard.type("uptime");
  await page.keyboard.press("Enter");
  await agent.next("shell-in");
  agent.send({
    op: "shell-out",
    shellData: b64("uptime\r\n 10:55:00 up 3 days\r\nnode@lab:~$ "),
  });
  await expect.poll(() => bufferText(page)).toContain("10:55:00 up 3 days");

  agent.close();
});
