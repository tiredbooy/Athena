import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

export function fixturePath(name: string): string {
  return join(dirname(fileURLToPath(import.meta.url)), "..", "..", "testdata", name);
}

export function loadFixture(name: string): string {
  return readFileSync(fixturePath(name), "utf8");
}
