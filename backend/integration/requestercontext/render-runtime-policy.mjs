#!/usr/bin/env node

import fs from "node:fs";
import path from "node:path";

const [templateArg, outputArg] = process.argv.slice(2);
const botId = (process.env.LUMI_BOT_ID ?? "").trim();
const userId = (process.env.LUMI_TEST_USER_ID ?? "").trim();

if (!templateArg || !outputArg || !botId || !userId) {
  console.error("usage: LUMI_BOT_ID=... LUMI_TEST_USER_ID=... render-runtime-policy.mjs TEMPLATE OUTPUT");
  process.exit(2);
}

const templatePath = path.resolve(templateArg);
const outputPath = path.resolve(outputArg);
const policy = JSON.parse(fs.readFileSync(templatePath, "utf8"));
const enabledUsers = policy.users.filter((user) => user.enabled === true);
if (enabledUsers.length !== 1) {
  throw new Error(`template must contain exactly one enabled user, found ${enabledUsers.length}`);
}

policy.botId = botId;
enabledUsers[0].userId = userId;
enabledUsers[0].displayName = "E2E Test User";

fs.mkdirSync(path.dirname(outputPath), { recursive: true, mode: 0o700 });
fs.writeFileSync(outputPath, `${JSON.stringify(policy, null, 2)}\n`, { mode: 0o600 });
fs.chmodSync(outputPath, 0o600);
console.log(`rendered Policy v${policy.version} with ${policy.users.length} users and ${enabledUsers.length} enabled user`);
