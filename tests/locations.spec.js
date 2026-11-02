import net from "node:net";
import { test, expect } from "@playwright/test";

async function connectAgent(hostname, location) {
  const socket = net.connect(34117, "127.0.0.1");
  await new Promise((resolve) => socket.on("connect", resolve));
  const send = (obj) => socket.write(JSON.stringify(obj) + "\n");
  send({ op: "enroll", hostname, os: "linux", arch: "amd64", version: "2.0.0", locationCapable: !!location });
  await new Promise((resolve) => socket.once("data", resolve));
  send({ op: "beat", cpuPercent: 4, memUsed: 100, memTotal: 800, uptimeSec: 3, location, locationCapable: !!location });
  return socket;
}

test("client-reported locations map LAN connections automatically and group active clients", async ({ page, request }) => {
  await request.post("/api/Reset", { data: {} });
  await page.goto("/");
  const location = { name: "Client city", lat: 51.5, lon: -0.12 };
  const first = await connectAgent("located-first", location);
  const second = await connectAgent("located-second", location);
  try {
    await expect(page.locator("#markers .marker-count")).toHaveText("2", { timeout: 12000 });
    await expect(page.locator("#markers title")).toContainText("Client city");
    await expect(page.locator("#unlocated-clients")).toBeHidden();
    await page.getByRole("button", { name: "Inspect located-first", exact: true }).click();
    await expect(page.locator("#detail-metrics")).toContainText("Client public-IP lookup");
    await page.keyboard.press("Escape");
    first.end();
    await expect(page.locator("#markers .marker-count")).toHaveText("1", { timeout: 12000 });
    second.end();
    await expect(page.locator("#markers [role=button]")).toHaveCount(0, { timeout: 12000 });
  } finally { first.destroy(); second.destroy(); }
});

test("private clients are grouped automatically; network overrides remain optional", async ({ page, request }) => {
  await request.post("/api/Reset", { data: {} });
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.setViewportSize({ width: 1440, height: 980 });
  await page.goto("/");

  const socket = await connectAgent("vlan-host");
  await expect(page.locator("#client-rows")).toContainText("vlan-host", { timeout: 12000 });

  await expect(page.locator("#atlas-prompt")).toHaveCount(0);
  await expect(page.locator("#unlocated-clients")).toBeVisible();
  await expect(page.locator("#unlocated-count")).toHaveText("1");

  // An optional override applies once to the whole network, never per client.
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  await page.locator("#manage-locations").click();
  await expect(page.locator("#locations-dialog")).toBeVisible();
  // The network was suggested from the agent address.
  await expect(page.locator("#site-cidr")).toHaveValue("127.0.0.0/24");
  await page.locator("#site-name").fill("Lab Rack");

  // Choose a position by clicking the map instead of typing coordinates.
  await page.locator("#pick-on-map").click();
  await expect(page.locator("#placing-hint")).toBeVisible();
  const map = page.locator("#atlas");
  await map.click({ position: { x: 500, y: 160 } });
  await expect(page.locator("#locations-dialog")).toBeVisible();
  await expect(page.locator("#site-lat")).not.toHaveValue("");
  await expect(page.locator("#site-lon")).not.toHaveValue("");

  await page.locator("#locations-form").getByRole("button", { name: "Add location" }).click();

  await expect(page.locator("#markers [role=button]")).toHaveCount(1, { timeout: 12000 });
  await expect(page.locator("#markers .marker-id")).toHaveCount(0);
  await expect(page.locator("#markers title")).toContainText("Lab Rack");
  await expect(page.locator("#markers .marker-count")).toHaveText("1");
  await expect(page.locator("#unlocated-clients")).toBeHidden();
  expect(errors).toEqual([]);
  socket.end();
  await expect(page.locator("#markers [role=button]")).toHaveCount(0, { timeout: 12000 });
});

test("adding a location with only a name uses the agent network", async ({ page, request }) => {
  await request.post("/api/Reset", { data: {} });
  await page.goto("/");
  const socket = await connectAgent("no-cidr");
  await expect(page.locator("#client-rows")).toContainText("no-cidr", { timeout: 12000 });

  const created = await request.post("/api/AddSite", {
    data: { name: "Auto Range", cidr: "", lat: 51.5, lon: -0.12 },
  });
  expect(created.ok()).toBe(true);
  const snap = await created.json();
  expect(snap.sites[0].cidr).toBe("127.0.0.0/24");

  await expect(page.locator("#markers [role=button]")).toHaveCount(1, { timeout: 12000 });
  await expect(page.locator("#markers .marker-count")).toHaveText("1");
  socket.end();
});
