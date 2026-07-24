import assert from "node:assert/strict";
import { afterEach, test } from "node:test";
import {
  configureMatterJsLogging,
  resetMatterJsLoggingForTests,
  resolveMatterJsLoggingOptions,
  type MatterJsLogMessage,
  type MatterJsLoggingApi,
} from "../src/runtime/matter-js-logging.js";

const LEVEL = {
  DEBUG: 0,
  INFO: 1,
  NOTICE: 2,
  WARN: 3,
  ERROR: 4,
  FATAL: 5,
  names: ["debug", "info", "notice", "warn", "error", "fatal"],
} as const;

afterEach(() => {
  resetMatterJsLoggingForTests();
  delete process.env.HOMELOOM_MATTER_LOG_LEVEL;
  delete process.env.HOMELOOM_MATTER_LOG_FORMAT;
  delete process.env.MATTER_LOG_LEVEL;
  delete process.env.MATTER_LOG_FORMAT;
});

test("resolveMatterJsLoggingOptions defaults to quiet JSON logs", () => {
  const resolved = resolveMatterJsLoggingOptions({});
  assert.equal(resolved.level, "notice");
  assert.equal(resolved.format, "json");
  assert.equal(resolved.facilityLevels.Endpoint, "warn");
  assert.equal(resolved.facilityLevels.MdnsService, "warn");
});

test("resolveMatterJsLoggingOptions honors HomeLoom env over matter env", () => {
  const resolved = resolveMatterJsLoggingOptions({
    HOMELOOM_MATTER_LOG_LEVEL: "error",
    HOMELOOM_MATTER_LOG_FORMAT: "plain",
    MATTER_LOG_LEVEL: "debug",
    MATTER_LOG_FORMAT: "ansi",
  });
  assert.equal(resolved.level, "error");
  assert.equal(resolved.format, "plain");
  assert.equal(resolved.facilityLevels.Endpoint, "warn");
});

test("debug level keeps facility filters open", () => {
  const resolved = resolveMatterJsLoggingOptions({
    HOMELOOM_MATTER_LOG_LEVEL: "debug",
  });
  assert.equal(resolved.level, "debug");
  assert.deepEqual(resolved.facilityLevels, {});
});

test("configureMatterJsLogging writes structured stderr JSON", () => {
  const writes: string[] = [];
  const originalWrite = process.stderr.write.bind(process.stderr);
  process.stderr.write = ((chunk: string | Uint8Array) => {
    writes.push(typeof chunk === "string" ? chunk : Buffer.from(chunk).toString("utf8"));
    return true;
  }) as typeof process.stderr.write;

  try {
    const envVars = new Map<string, unknown>();
    const destination = createDestination();
    const destinations = { default: destination };
    const logLevel = ((level: string | number) => {
      if (typeof level === "number") {
        return level;
      }
      const index = LEVEL.names.indexOf(level as (typeof LEVEL.names)[number]);
      if (index < 0) {
        throw new Error(`bad level ${level}`);
      }
      return index;
    }) as MatterJsLoggingApi["LogLevel"];
    Object.assign(logLevel, {
      names: [...LEVEL.names],
      0: "debug",
      1: "info",
      2: "notice",
      3: "warn",
      4: "error",
      5: "fatal",
    });

    const api: MatterJsLoggingApi = {
      Logger: {
        level: LEVEL.DEBUG,
        format: "ansi",
        facilityLevels: {},
        destinations,
      },
      LogLevel: logLevel,
      LogFormat: Object.assign(
        (format: string) => {
          if (format === "plain") {
            return (diagnostic: unknown) =>
              Array.isArray(diagnostic) ? diagnostic.map(String).join(" ") : String(diagnostic);
          }
          return () => format;
        },
        {
          PLAIN: "plain",
          ANSI: "ansi",
          formats: {
            plain: (diagnostic: unknown) =>
              Array.isArray(diagnostic) ? diagnostic.map(String).join(" ") : String(diagnostic),
            ansi: () => "ansi",
          },
        },
      ),
      Environment: {
        default: {
          vars: {
            set(name: string, value: unknown) {
              envVars.set(name, value);
            },
          },
        },
      },
    };

    const resolved = configureMatterJsLogging(api, { level: "notice", format: "json" });
    assert.equal(resolved.level, "notice");
    assert.equal(api.Logger.level, LEVEL.NOTICE);
    assert.equal(destination.level, LEVEL.NOTICE);
    assert.equal(destination.facilityLevels.Endpoint, LEVEL.WARN);
    assert.equal(envVars.get("log.level"), "notice");
    assert.equal(envVars.get("log.format"), "plain");
    assert.equal(process.env.MATTER_LOG_LEVEL, "notice");
    assert.equal(process.env.MATTER_LOG_FORMAT, "plain");
    assert.equal(process.env.HOMELOOM_MATTER_LOG_LEVEL, "notice");
    assert.equal(process.env.HOMELOOM_MATTER_LOG_FORMAT, "json");

    destination.add(
      message({
        level: LEVEL.NOTICE,
        facility: "ServerNode",
        values: ["bridge ready", 42],
      }),
    );

    assert.equal(writes.length, 1);
    const payload = JSON.parse(writes[0] ?? "{}") as Record<string, string>;
    assert.equal(payload.level, "NOTICE");
    assert.equal(payload.component, "matter-js");
    assert.equal(payload.facility, "ServerNode");
    assert.equal(payload.msg, "bridge ready 42");
    assert.match(payload.time ?? "", /^\d{4}-\d{2}-\d{2}T/);
  } finally {
    process.stderr.write = originalWrite;
  }
});

function createDestination() {
  return {
    level: LEVEL.DEBUG as number,
    facilityLevels: {} as Record<string, number>,
    format(message: MatterJsLogMessage) {
      return message.values.map(String).join(" ");
    },
    write(_text: string, _message: MatterJsLogMessage) {},
    add(message: MatterJsLogMessage) {
      this.write(this.format(message), message);
    },
  };
}

function message(partial: Partial<MatterJsLogMessage>): MatterJsLogMessage {
  return {
    now: new Date("2026-07-24T04:00:00.000Z"),
    level: LEVEL.INFO,
    facility: "Test",
    prefix: "",
    values: [],
    ...partial,
  };
}
