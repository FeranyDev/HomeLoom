import { runCli } from "./src/cli.js";

void runCli(process.argv.slice(2)).catch((error: unknown) => {
  const message = error instanceof Error ? error.stack ?? error.message : String(error);
  process.stderr.write(`${message}\n`);
  process.exitCode = 1;
});
