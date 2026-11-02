import { test, expect } from "@playwright/test";

test("large registry render and scroll", async ({ page }, testInfo) => {
  const agents = Array.from({ length: 10000 }, (_, i) => ({
    id: `agent-${i}`, hostname: `managed-host-${i}`, os: "linux", arch: "amd64",
    version: "v1", status: "online", cpu: 3, memUsed: 100, memTotal: 800,
    uptime: 10, remote: "10.0.0.1", lastSeen: "10:00:00", updateCapable: true,
  }));
  const snapshot = { agents, events: [], points: [], unplaced: 10000, sites: [], history: [10000],
    elapsed: 10, serverVersion: "2.0.0", clientVersion: "v2", listenAddr: "127.0.0.1:34117", advertiseURL: "127.0.0.1:34117", listenerError: "",
    settings: { listenAddr: "127.0.0.1:34117", advertiseURL: "127.0.0.1:34117", maxClients: 20000, clientDefaults: { retrySeconds: 10, maxRetrySeconds: 60, heartbeatSeconds: 15, connectTimeoutSeconds: 5 } },
  };
  await page.route("**/api/*", route => route.fulfill({ json: snapshot }));
  const start = Date.now();
  await page.goto("/");
  await expect(page.locator("#online-count")).toHaveText("10000");
  const metrics = await page.evaluate(async () => {
    const frames = [];
    let previous = performance.now();
    for (let i = 0; i < 60; i++) {
      await new Promise(resolve => requestAnimationFrame(resolve));
      const now = performance.now(); frames.push(now - previous); previous = now;
      window.scrollTo(0, i * 70);
    }
    return { rows: document.querySelectorAll("#client-rows tr").length, versions: document.querySelectorAll("#version-list .version-row").length,
      maxFrameMs: Math.round(Math.max(...frames)), framesOver32ms: frames.filter(n => n > 32).length };
  });
  metrics.elapsedMs = Date.now() - start;
  await testInfo.attach("render-scroll-metrics", { body: JSON.stringify(metrics), contentType: "application/json" });
  console.log("Registry performance", metrics);
  expect(metrics.rows).toBeLessThanOrEqual(100);
  expect(metrics.versions).toBe(0);
  await expect(page.locator("#unlocated-count")).toHaveText("10000");
  await page.locator("#clients-next").click();
  await expect(page.locator("#clients-page")).toHaveText("2 / 100");
  await page.getByRole("button", { name: "Version manager", exact: true }).click();
  await expect(page.locator("#version-list .version-row")).toHaveCount(100);
});
