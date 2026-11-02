import { test, expect } from "@playwright/test";

test("settings clears offline clients only after confirmation", async ({ page, request }) => {
  const snapshot = await (await request.post("/api/Snapshot", { data: {} })).json();
  const base = { os: "linux", arch: "amd64", version: "v1", cpu: 0, memUsed: 0, memTotal: 0, uptime: 0 };
  const online = { ...base, id: "online", hostname: "keep-connected", status: "online" };
  const offline = { ...base, id: "offline", hostname: "remove-disconnected", status: "offline" };
  let calls = 0;
  await page.route("**/api/Snapshot", route => route.fulfill({ json: { ...snapshot, agents: [online, offline] } }));
  await page.route("**/api/ClearOfflineClients", route => {
    calls++;
    return route.fulfill({ json: { removed: 1, snapshot: { ...snapshot, agents: [online] } } });
  });
  await page.goto("/");
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  const clear = page.locator("#clear-offline-clients");
  await expect(clear).toHaveText("Clear all offline clients (1)");
  page.once("dialog", dialog => dialog.dismiss());
  await clear.click();
  expect(calls).toBe(0);
  page.once("dialog", dialog => dialog.accept());
  await clear.click();
  await expect(page.locator("#settings-status")).toContainText("Cleared 1 offline client");
  await expect(clear).toBeDisabled();
  expect(calls).toBe(1);
  await page.getByRole("button", { name: "Clients", exact: true }).click();
  await expect(page.locator("#client-rows")).toContainText("keep-connected");
  await expect(page.locator("#client-rows")).not.toContainText("remove-disconnected");
});

test("settings save, validation and new-build defaults", async ({ page, request }) => {
  const original = await (await request.post("/api/Snapshot", { data: {} })).json();
  try {
    await page.goto("/");
    await page.getByRole("button", { name: "Settings", exact: true }).click();
    await expect(page.locator("#settings-retry")).toHaveValue(String(original.settings.clientDefaults.retrySeconds));
    await page.locator("#settings-retry").fill("12");
    await page.locator("#settings-max-retry").fill("10");
    await page.getByRole("button", { name: "Save settings", exact: true }).click();
    await expect(page.locator("#settings-error")).toContainText("maximum retry");
    await page.locator("#settings-max-retry").fill("60");
    await page.getByRole("button", { name: "Save settings", exact: true }).click();
    await expect(page.locator("#settings-status")).toContainText("saved successfully");
    await page.locator("#add-button").click();
    await expect(page.locator("#build-retry")).toHaveValue("12");
    await page.keyboard.press("Escape");
    await page.screenshot({ path: "test-results/settings-desktop.png", fullPage: true });
    await page.setViewportSize({ width: 768, height: 1100 });
    await page.screenshot({ path: "test-results/settings-tablet.png", fullPage: true });
    await page.setViewportSize({ width: 390, height: 1100 });
    await page.screenshot({ path: "test-results/settings-mobile.png", fullPage: true });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  } finally {
    await request.post("/api/SaveSettings", { data: { settings: original.settings } });
  }
});

test("client creation shows a persistent success message and sends build settings", async ({ page, request }) => {
  const snapshot = await (await request.post("/api/Snapshot", { data: {} })).json();
  let submitted;
  let completed = false;
  await page.route("**/api/BuildClientConfigured", async route => {
    submitted = route.request().postDataJSON();
    completed = true;
    await route.fulfill({ json: { path: "/managed/atlas" } });
  });
  await page.route("**/api/Snapshot", route => route.fulfill({ json: { ...snapshot, clientVersion: completed ? "v7" : snapshot.clientVersion } }));
  await page.goto("/");
  await page.locator("#add-button").click();
  await expect(page.locator("#build-retry")).toHaveValue(String(snapshot.settings.clientDefaults.retrySeconds));
  await page.locator("#build-retry").fill("10");
  await page.getByRole("button", { name: "Build client", exact: true }).click();
  await expect(page.locator("#build-success")).toContainText("v7 created successfully");
  await expect(page.locator("#build-latest-version")).toHaveText("v7");
  await expect(page.getByRole("button", { name: "Build another release" })).toBeEnabled();
  expect(submitted.config.retrySeconds).toBe(10);
  expect(submitted.config.heartbeatSeconds).toBe(snapshot.settings.clientDefaults.heartbeatSeconds);
  await page.screenshot({ path: "test-results/build-success.png" });
});

test("bulk update action queues eligible clients with confirmation", async ({ page, request }) => {
  const snapshot = await (await request.post("/api/Snapshot", { data: {} })).json();
  const agent = { id: "managed-1", hostname: "managed-1", os: "linux", arch: "amd64", version: "v1", status: "online", updateCapable: true, cpu: 0, memUsed: 0, memTotal: 0, uptime: 0 };
  let requested = false;
  const state = { ...snapshot, clientVersion: "v2", agents: [agent] };
  await page.route("**/api/Snapshot", route => route.fulfill({ json: state }));
  await page.route("**/api/UpdateAllClients", route => { requested = true; return route.fulfill({ json: { queued: 1, skipped: 0, snapshot: { ...state, agents: [{ ...agent, updateStatus: "Queued" }] } } }); });
  await page.goto("/");
  page.on("dialog", dialog => dialog.accept());
  await page.locator("#update-all-clients").click();
  await expect(page.locator("#toast")).toContainText("Updates queued for 1 clients");
  expect(requested).toBe(true);
  await expect(page.locator("#update-all-clients")).toBeDisabled();
});
