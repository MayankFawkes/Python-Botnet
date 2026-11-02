import net from "node:net";
import { test, expect } from "@playwright/test";

function tcpSession(messages) {
  return new Promise((resolve, reject) => {
    const socket = net.connect(34117, "127.0.0.1");
    const lines = [];
    let buf = "";
    let settled = false;
    const finish = (error) => {
      if (settled) return;
      settled = true;
      socket.setTimeout(0);
      if (error) {
        socket.destroy();
        reject(error);
        return;
      }
      resolve({
        lines,
        close() {
          socket.end();
        },
      });
    };
    socket.setTimeout(5000, () => finish(new Error("agent TCP timed out")));
    socket.on("error", finish);
    socket.on("data", (chunk) => {
      buf += chunk.toString();
      let index;
      while ((index = buf.indexOf("\n")) >= 0) {
        try {
          lines.push(JSON.parse(buf.slice(0, index)));
        } catch (error) {
          finish(error);
          return;
        }
        buf = buf.slice(index + 1);
        if (lines.length >= messages.length) finish();
      }
    });
    socket.on("connect", () => {
      socket.write(messages.map((message) => JSON.stringify(message) + "\n").join(""));
    });
  });
}

test.beforeEach(async ({ request }) => {
  const response = await request.post("/api/Reset", { data: {} });
  expect(response.ok()).toBe(true);
});

test("empty console, atlas, and agent registration", async ({ page }) => {
  const externalRequests = [],
    errors = [];
  page.on("request", (request) => {
    if (!request.url().startsWith("http://127.0.0.1:34115/"))
      externalRequests.push(request.url());
  });
  page.on("pageerror", (error) => errors.push(error.message));
  await page.goto("/");
  await expect(page.locator("#online-count")).toHaveText("0");
  await expect(page.locator("#land path")).toHaveCount(127);
  await expect(page.locator("#markers [role=button]")).toHaveCount(0);
  await expect(page.locator("#client-rows")).toContainText("No agents yet");
  await expect(page.locator("#running-label")).toHaveText("LISTENING");

  const session = await tcpSession([
    {
      op: "enroll",
      hostname: "lab-node",
      os: "linux",
      arch: "amd64",
      version: "2.0.0",
    },
    { op: "beat", cpuPercent: 12.5, memUsed: 1000, memTotal: 8000, uptimeSec: 4 },
  ]);
  expect(session.lines[0].op).toBe("enrolled");
  expect(session.lines[1].op).toBe("ok");
  await expect(page.locator("#client-rows")).toContainText("lab-node", {
    timeout: 12000,
  });
  await expect(page.locator("#online-count")).toHaveText("1");
  await page.locator("#search").fill("missing-host");
  await expect(page.locator("#client-rows")).toContainText("No agents match");
  await page.locator("#search").fill("");
  await page.getByRole("button", { name: "Inspect lab-node", exact: true }).click();
  await expect(page.locator("#client-dialog")).toBeVisible();
  await expect(page.locator("#detail-metrics")).toContainText("12.5%");
  await expect(page.locator("#client-rows")).toContainText("v2.0.0");
  await expect(page.locator("#detail-meta")).toContainText("v2.0.0");
  await expect(page.locator("#update-agent")).toBeDisabled();
  session.close();
  await page.screenshot({
    path: "test-results/overview-desktop.png",
    fullPage: true,
  });
  expect(externalRequests).toEqual([]);
  expect(errors).toEqual([]);
});

test("declared network places agents on the atlas", async ({ page, request }) => {
  await page.goto("/");
  await expect(page.locator("#online-count")).toHaveText("0");
  const site = await request.post("/api/AddSite", {
    data: { name: "Rack A", cidr: "127.0.0.0/8", lat: 28.6139, lon: 77.209 },
  });
  expect(site.ok()).toBe(true);
  const session = await tcpSession([
    {
      op: "enroll",
      hostname: "vlan-node",
      os: "linux",
      arch: "amd64",
      version: "2.0.0",
    },
    { op: "beat", cpuPercent: 3, memUsed: 100, memTotal: 800, uptimeSec: 2 },
  ]);
  expect(session.lines[0].op).toBe("enrolled");
  await expect(page.locator("#markers [role=button]")).toHaveCount(1, {
    timeout: 12000,
  });
  await expect(page.locator("#markers .marker-count")).toHaveText("1");
  await expect(page.locator("#markers .marker-id")).toHaveCount(0);
  await expect(page.locator("#markers title")).toContainText("Rack A");
  await expect(page.locator("#geo-caption")).toContainText("1 LOCATION");
  session.close();
});

test("build client dialog validates server list", async ({ page, request }) => {
  await page.goto("/");
  await page.locator("#add-button").click();
  const snapshot = await (await request.post("/api/Snapshot", { data: {} })).json();
  await expect(page.locator("#build-latest-version")).toHaveText(snapshot.clientVersion);
  await page.locator("#extra-servers").fill("not-a-valid-address");
  await page.getByRole("button", { name: "Build client" }).click();
  await expect(page.locator("#form-error")).toContainText("host:port");
  await page.keyboard.press("Escape");
});

test("version view shows an older reported build", async ({ page }) => {
  await page.goto("/");
  await expect(page.locator("#online-count")).toHaveText("0");
  const session = await tcpSession([
    {
      op: "enroll",
      hostname: "old-build",
      os: "linux",
      arch: "amd64",
      version: "1.0.0",
    },
  ]);
  expect(session.lines[0].op).toBe("enrolled");
  session.close();
  await page.getByRole("button", { name: "Version manager", exact: true }).click();
  await expect(page.locator("#version-list")).toContainText("old-build", {
    timeout: 12000,
  });
  await expect(page.locator("#version-list")).toContainText("Earlier version");
  await expect(page.locator("#version-value")).toHaveText("0");
});

for (const width of [768, 390]) {
  test(`layout at ${width}px and keyboard dialog dismissal`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 1100 });
    await page.goto("/");
    await expect(page.locator("#land path")).toHaveCount(127);
    await page.screenshot({
      path: `test-results/overview-${width}.png`,
      fullPage: true,
    });
    const overflow = await page.evaluate(() => ({
      width: window.innerWidth,
      scroll: document.documentElement.scrollWidth,
      elements: [...document.querySelectorAll("body *")]
        .filter((element) => {
          const rect = element.getBoundingClientRect();
          return (
            rect.width &&
            rect.right > innerWidth &&
            !element.closest(".table-scroll") &&
            !element.closest("svg")
          );
        })
        .map(
          (element) =>
            `${element.tagName}.${element.className}: ${element.getBoundingClientRect().width}`,
        )
        .slice(0, 15),
    }));
    expect(overflow.scroll, JSON.stringify(overflow)).toBeLessThanOrEqual(
      width,
    );
    await page.locator("#add-button").click();
    await expect(page.locator("#extra-servers")).toBeFocused();
    await page.keyboard.press("Escape");
    await expect(page.locator("#add-dialog")).not.toBeVisible();
    await expect(page.locator("#add-button")).toBeFocused();
  });
}
