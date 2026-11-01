import { chromium } from "../frontend/node_modules/playwright/index.mjs";
import { fileURLToPath } from "node:url";
const repository = fileURLToPath(new URL("..", import.meta.url));
import fs from "node:fs";
import crypto from "node:crypto";
import assert from "node:assert/strict";
let raw = "";
for await (const chunk of process.stdin) raw += chunk;
const input = JSON.parse(raw);
raw = "";
const browser = await chromium.launch({
  executablePath: process.env.NSC_BROWSER_BINARY || undefined,
  headless: true,
  args: ["--no-sandbox"],
});
const checks = [];
try {
  const context = await browser.newContext({
    ignoreHTTPSErrors: true,
    locale: "en-US",
    viewport: { width: 1440, height: 1000 },
  });
  const page = await context.newPage();
  const failures = [];
  page.on("pageerror", (error) => failures.push(error.message));
  const headers = {
    Authorization: "Bearer " + input.password,
    "X-NSC-Reauthenticate": input.password,
  };
  const api = async (method, path, body, revision) => {
    const r = await context.request.fetch(input.url + "/api/v1" + path, {
      method,
      data: body,
      headers: {
        ...headers,
        ...(revision ? { "If-Match": '"rev-' + revision + '"' } : {}),
      },
    });
    assert(
      r.ok(),
      "fixture API request failed " + method + " " + path + " " + r.status(),
    );
    return (await r.json()).data;
  };
  await page.goto(input.url + "/login");
  await page.locator("#username").fill("admin");
  await page.locator("#password").fill(input.password);
  await Promise.all([
    page.waitForURL((url) => url.pathname === "/projects"),
    page.locator("button[type=submit]").click(),
  ]);
  await page.goto(input.url + "/projects/all/environments/all/domains");
  await page
    .getByRole("button", { name: "Manage DNS profiles", exact: true })
    .click();
  const panel = page.locator(".dns-profiles");
  await panel.locator("header button").click();
  const reference = "browser-he-" + crypto.randomBytes(4).toString("hex");
  const secret = () => crypto.randomBytes(24).toString("hex");
  const first = secret(),
    second = secret(),
    rotated = secret();
  await panel.locator("[name=reference]").fill(reference);
  await panel.locator("[name=provider]").selectOption("hurricane");
  await panel.locator("[name=token-domain]").fill("a.example.test");
  await panel.locator("[name=token-value]").fill(first);
  await panel.locator("[data-add-token]").click();
  await panel.locator("[name=token-domain]").nth(1).fill("b.example.test");
  await panel.locator("[name=token-value]").nth(1).fill(second);
  await panel.locator("[name=password]").fill(input.password);
  let response = page.waitForResponse(
    (r) =>
      r.url().endsWith("/dns-provider-profiles") &&
      r.request().method() === "POST",
  );
  await panel.locator("[type=submit]").click();
  assert.equal((await response).status(), 201);
  await panel.locator("form").waitFor({ state: "detached" });
  const row = panel.locator(".profile-row").filter({ hasText: reference });
  await row.waitFor();
  await row
    .getByRole("button", { name: "Edit credentials", exact: true })
    .click();
  await panel.locator("[name=token-domain]").nth(1).waitFor();
  assert.equal(
    await panel.locator("[name=token-value]").nth(0).inputValue(),
    "",
  );
  await panel.locator("[name=token-value]").nth(0).fill(rotated);
  await panel.locator("[name=password]").fill(input.password);
  response = page.waitForResponse(
    (r) =>
      r.url().includes("/dns-provider-profiles/" + reference) &&
      r.request().method() === "PATCH",
  );
  await panel.locator("[type=submit]").click();
  let saved = await response;
  assert.equal(saved.status(), 200);
  assert.deepEqual(saved.request().postDataJSON(), {
    credentials: { tokens: { "a.example.test": rotated } },
  });
  const body = await saved.text();
  assert(
    ![first, second, rotated].some((s) => body.includes(s)),
    "write response leaked a credential",
  );
  await panel.locator("form").waitFor({ state: "detached" });
  let detail = await api("GET", "/dns-provider-profiles/" + reference);
  assert.deepEqual(detail.token_domains, ["a.example.test", "b.example.test"]);
  checks.push(
    "browser_he_multiple_tokens_partial_rotation_and_redacted_response",
  );
  await row
    .getByRole("button", { name: "Edit credentials", exact: true })
    .click();
  await panel.locator("[name=token-domain]").nth(1).waitFor();
  const retained = secret();
  await panel.locator("[name=token-value]").nth(0).fill(retained);
  await api(
    "PATCH",
    "/dns-provider-profiles/" + reference,
    {
      credentials: {
        tokens: { "a.example.test": null, "c.example.test": secret() },
      },
    },
    detail.revision,
  );
  await panel.locator("[name=password]").fill(input.password);
  response = page.waitForResponse(
    (r) => r.request().method() === "PATCH" && r.url().includes(reference),
  );
  await panel.locator("[type=submit]").click();
  assert.equal((await response).status(), 412);
  assert.equal(
    await panel.locator("[name=token-value]").nth(0).inputValue(),
    retained,
  );
  assert.equal(await panel.locator("[name=password]").inputValue(), "");
  await panel
    .getByRole("button", {
      name: "Reload latest metadata; keep input",
      exact: true,
    })
    .click();
  await panel.locator("[name=token-domain]").nth(2).waitFor();
  assert.equal(
    await panel.locator("[name=token-value]").nth(0).inputValue(),
    retained,
  );
  await panel.locator(".token-row").nth(1).getByRole("button").click();
  await panel.locator("[name=password]").fill(input.password);
  response = page.waitForResponse(
    (r) => r.request().method() === "PATCH" && r.url().includes(reference),
  );
  await panel.locator("[type=submit]").click();
  assert.equal((await response).status(), 200);
  await panel.locator("form").waitFor({ state: "detached" });
  detail = await api("GET", "/dns-provider-profiles/" + reference);
  assert.deepEqual(detail.token_domains, ["a.example.test", "c.example.test"]);
  checks.push(
    "browser_stale_etag_preserves_input_explicit_reload_and_single_token_removal",
  );
  for (const locale of JSON.parse(
    fs.readFileSync(repository + "/internal/i18n/locales.json", "utf8"),
  )) {
    await page.locator("#workspace-language").selectOption(locale.code);
    const messages = JSON.parse(
      fs.readFileSync(
        repository +
          "/frontend/src/shared/i18n/locales/" +
          locale.code +
          ".json",
        "utf8",
      ),
    );
    await row
      .getByRole("button", {
        name: messages["dnsProfiles.rotate"],
        exact: true,
      })
      .waitFor();
  }
  await page.locator("#workspace-language").selectOption("en");
  await row
    .getByRole("button", { name: "Edit credentials", exact: true })
    .waitFor();
  await row
    .getByRole("button", { name: "Edit credentials", exact: true })
    .click();
  await panel.locator("[name=token-domain]").nth(1).waitFor();
  for (const field of await panel.locator("input[type=password]").all())
    assert.equal(await field.inputValue(), "");
  await page.setViewportSize({ width: 390, height: 844 });
  if (
    await page
      .locator(".sidebar")
      .evaluate((element) => element.classList.contains("open"))
  ) {
    await page.locator("button[data-action=menu]").click();
  }
  await page.waitForFunction(
    () => document.querySelector(".sidebar").getBoundingClientRect().right <= 1,
  );
  await panel.locator("form").scrollIntoViewIfNeeded();
  assert.equal(
    await page.evaluate(
      () => document.documentElement.scrollWidth > innerWidth + 1,
    ),
    false,
    "credential editor overflows mobile viewport",
  );
  await page.screenshot({ path: input.output + "/dns-mobile.png" });
  await panel.locator("header button").click();
  await page.setViewportSize({ width: 1440, height: 1000 });
  checks.push(
    "browser_eight_languages_no_secret_rehydration_and_mobile_layout",
  );
  await page.goto(input.url + input.domain_path);
  await page
    .locator("#main")
    .getByText("/__nsc_validation/", { exact: true })
    .first()
    .waitFor();
  await page
    .locator(`[data-action=route-edit][data-id="${input.validation_route}"]`)
    .first()
    .click();
  assert.equal(
    await page.locator("#rf-service").inputValue(),
    input.secondary_service,
    "cross-environment target disappeared",
  );
  await page.locator("#rf-timeout").fill("91");
  await page.locator("[data-action=route-apply]").click();
  await page.locator("[data-action=validate]").first().click();
  await page
    .getByText("Every Gateway validated the candidate configuration.", {
      exact: true,
    })
    .waitFor({ timeout: 90000 });
  assert.equal(
    await page.locator("#overlay .callout.red").count(),
    0,
    "same-Pool existing route blocked validation",
  );
  await page.screenshot({ path: input.output + "/routing-validation.png" });
  await page.locator("#overlay [data-action=close]").first().click();
  checks.push(
    "browser_cross_environment_draft_retains_service_refreshes_and_real_nginx_validation",
  );
  // A failed instance read must be unknown, even when the backend validation succeeds.
  const instances =
    "**/api/v1/services/" + input.primary_service + "/instances*";
  await page.route(instances, (route) =>
    route.fulfill({
      status: 503,
      contentType: "application/problem+json",
      body: JSON.stringify({ code: "fixture.unavailable", status: 503 }),
    }),
  );
  await page.locator("[data-action=validate]").first().click();
  await page
    .getByText("Every Gateway validated the candidate configuration.", {
      exact: true,
    })
    .waitFor({ timeout: 90000 });
  const overlay = await page.locator("#overlay").innerText();
  assert(
    overlay.includes("Ready Endpoint state is unknown"),
    "failed observation was not marked unknown",
  );
  assert(
    !overlay.includes("This internal Service has no Ready Endpoint."),
    "failed read invented zero Ready",
  );
  await page.unroute(instances);
  checks.push("browser_failed_endpoint_read_is_unknown_with_validation_retry");
  await page
    .getByRole("button", { name: "Refresh and validate again", exact: true })
    .click();
  await page
    .getByText("Every Gateway validated the candidate configuration.", {
      exact: true,
    })
    .waitFor({ timeout: 90000 });
  await page.locator("#overlay [data-action=close]").first().click();
  await page.locator("[data-action=publish]").first().click();
  await page.locator("#publish-ack").check();
  await page.locator("[data-action=release-start]").click();
  await page
    .locator("#overlay .pool-release-overall .badge.green")
    .waitFor({ timeout: 90000 });
  checks.push("browser_explicit_publish_confirms_gateway_loaded_version");
  const persisted = (await api("GET", "/routes?limit=200")).find(
    (r) => r.id === input.validation_route,
  );
  assert.equal(persisted.backend.service_id, input.secondary_service);
  assert.equal(persisted.read_timeout_seconds, 91);
  assert.deepEqual(failures, [], "browser JavaScript errors occurred");
  await context.close();
  console.log(JSON.stringify({ status: "PASS", checks }));
} catch (error) {
  console.error(error.stack);
  process.exitCode = 1;
} finally {
  await browser.close();
}
