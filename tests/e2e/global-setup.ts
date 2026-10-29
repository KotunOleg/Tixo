import { execFile } from "node:child_process";
import { writeFile } from "node:fs/promises";
import { promisify } from "node:util";

import type { FullConfig } from "@playwright/test";

const execFileAsync = promisify(execFile);

export const e2eOrg = process.env.E2E_ORG ?? "e2e-ci";

export default async function globalSetup(config: FullConfig) {
  const bin = process.env.E2E_TIXO_BIN;
  if (!bin) {
    throw new Error("E2E_TIXO_BIN must point at a tixo binary built with `go build -cover`");
  }
  const email = process.env.E2E_EMAIL ?? "e2e@example.com";
  const { stdout } = await execFileAsync(bin, ["seed-session", "--email", email, "--org", e2eOrg]);
  const match = stdout.match(/^cookie: tixo_session=(\S+)$/m);
  if (!match) {
    throw new Error(`seed-session did not print a cookie line, got:\n${stdout}`);
  }

  const baseURL = config.projects[0]?.use?.baseURL ?? process.env.E2E_BASE_URL ?? "http://localhost:8080";
  const url = new URL(baseURL);
  await writeFile(
    "storageState.json",
    JSON.stringify({
      cookies: [
        {
          name: "tixo_session",
          value: match[1],
          domain: url.hostname,
          path: "/",
          expires: -1,
          httpOnly: true,
          secure: url.protocol === "https:",
          sameSite: "Lax",
        },
      ],
      origins: [],
    }),
  );
}
