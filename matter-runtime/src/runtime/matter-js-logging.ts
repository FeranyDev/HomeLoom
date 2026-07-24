export type MatterJsLogLevelName =
  | "debug"
  | "info"
  | "notice"
  | "warn"
  | "error"
  | "fatal";

export type MatterJsLogFormatName = "json" | "plain" | "ansi";

export interface MatterJsLoggingOptions {
  level?: string;
  format?: string;
  facilityLevels?: Record<string, string>;
}

export interface ResolvedMatterJsLogging {
  level: MatterJsLogLevelName;
  format: MatterJsLogFormatName;
  facilityLevels: Record<string, MatterJsLogLevelName>;
}

export interface MatterJsLogMessage {
  now: Date;
  level: number;
  facility: string;
  prefix: string;
  values: unknown[];
}

/**
 * Structural subset of the matter.js logging API. Kept deliberately loose so
 * @matter/main's branded LogLevel type does not leak into HomeLoom helpers.
 */
export interface MatterJsLoggingApi {
  readonly Logger: {
    level: unknown;
    format: unknown;
    facilityLevels: unknown;
    destinations: Record<string, MatterJsLogDestinationLike>;
  };
  readonly LogLevel: MatterJsLogLevelLike;
  readonly LogFormat: MatterJsLogFormatLike;
  readonly Environment?: {
    readonly default: {
      readonly vars: {
        set(name: string, value: unknown): void;
      };
    };
  };
}

interface MatterJsLogDestinationLike {
  level: unknown;
  facilityLevels: Record<string, unknown>;
  format: (message: MatterJsLogMessage) => string;
  write: (text: string, message: MatterJsLogMessage) => void;
  add: (message: MatterJsLogMessage) => void;
}

interface MatterJsLogLevelLike {
  (level: string | number): number;
  readonly names: readonly string[];
  readonly [index: number]: string | undefined;
}

interface MatterJsLogFormatLike {
  (format: string): (diagnostic: unknown, indents?: number) => string;
  readonly PLAIN: string;
  readonly ANSI: string;
  readonly formats: Record<string, (diagnostic: unknown, indents?: number) => string>;
}

const DEFAULT_LEVEL: MatterJsLogLevelName = "notice";
const DEFAULT_FORMAT: MatterJsLogFormatName = "json";

/**
 * Facilities that stay noisy even near the default NOTICE threshold.
 * Pin them to WARN unless the operator explicitly opens debug/info.
 */
const QUIET_FACILITIES = [
  "Endpoint",
  "EndpointStore",
  "Behavior",
  "Transaction",
  "EventHandler",
  "InteractionServer",
  "ExchangeManager",
  "SecureChannelProtocol",
  "PeerAddress",
  "SessionManager",
  "MdnsService",
  "MdnsScanner",
  "MdnsBroadcaster",
  "Network",
  "ChannelManager",
  "CaseClient",
  "CaseServer",
  "PaseClient",
  "PaseServer",
] as const;

let configured = false;

export function resolveMatterJsLoggingOptions(
  env: NodeJS.ProcessEnv = process.env,
  overrides: MatterJsLoggingOptions = {},
): ResolvedMatterJsLogging {
  const level = normalizeLevel(
    overrides.level ??
      env.HOMELOOM_MATTER_LOG_LEVEL ??
      env.MATTER_LOG_LEVEL ??
      DEFAULT_LEVEL,
  );
  const format = normalizeFormat(
    overrides.format ??
      env.HOMELOOM_MATTER_LOG_FORMAT ??
      env.MATTER_LOG_FORMAT ??
      DEFAULT_FORMAT,
  );

  const facilityLevels: Record<string, MatterJsLogLevelName> = {};
  if (level !== "debug" && level !== "info") {
    for (const facility of QUIET_FACILITIES) {
      facilityLevels[facility] = "warn";
    }
  }
  if (overrides.facilityLevels !== undefined) {
    for (const [facility, facilityLevel] of Object.entries(overrides.facilityLevels)) {
      facilityLevels[facility] = normalizeLevel(facilityLevel);
    }
  }

  return { level, format, facilityLevels };
}

/**
 * Configure matter.js logging for this process.
 *
 * Defaults intentionally differ from the SDK:
 * - level NOTICE (SDK defaults to DEBUG)
 * - JSON lines on stderr (SDK defaults to ANSI console writers that mix with stdout)
 */
export function configureMatterJsLogging(
  api: MatterJsLoggingApi,
  options: MatterJsLoggingOptions = {},
): ResolvedMatterJsLogging {
  const resolved = resolveMatterJsLoggingOptions(process.env, options);
  applyMatterJsLogging(api, resolved);
  configured = true;
  return resolved;
}

export function configureMatterJsLoggingOnce(
  api: MatterJsLoggingApi,
  options: MatterJsLoggingOptions = {},
): ResolvedMatterJsLogging {
  if (configured) {
    return resolveMatterJsLoggingOptions(process.env, options);
  }
  return configureMatterJsLogging(api, options);
}

/** Test helper so suite runs stay isolated. */
export function resetMatterJsLoggingForTests(): void {
  configured = false;
}

function applyMatterJsLogging(
  api: MatterJsLoggingApi,
  resolved: ResolvedMatterJsLogging,
): void {
  const { Logger, LogLevel, LogFormat, Environment } = api;
  const destination = Logger.destinations.default;
  if (destination === undefined) {
    throw new Error("matter.js default log destination is missing");
  }

  const numericLevel = LogLevel(resolved.level);
  const facilityLevels: Record<string, number> = {};
  for (const [facility, level] of Object.entries(resolved.facilityLevels)) {
    facilityLevels[facility] = LogLevel(level);
  }

  // Seed official MATTER_* variables before the Node.js platform boots so
  // Environment.default reloads keep the quieter level/format.
  const matterFormat = resolved.format === "json" ? LogFormat.PLAIN : resolved.format;
  process.env.MATTER_LOG_LEVEL = resolved.level;
  process.env.MATTER_LOG_FORMAT = matterFormat;
  if (process.env.HOMELOOM_MATTER_LOG_LEVEL === undefined) {
    process.env.HOMELOOM_MATTER_LOG_LEVEL = resolved.level;
  }
  if (process.env.HOMELOOM_MATTER_LOG_FORMAT === undefined) {
    process.env.HOMELOOM_MATTER_LOG_FORMAT = resolved.format;
  }

  // Align Environment variables so later env reloads cannot restore DEBUG/ANSI.
  Environment?.default.vars.set("log.level", resolved.level);
  Environment?.default.vars.set("log.format", matterFormat);

  Logger.level = numericLevel;
  Logger.facilityLevels = facilityLevels;
  destination.level = numericLevel;
  destination.facilityLevels = { ...destination.facilityLevels, ...facilityLevels };

  // Logger.format rewrites every destination.format; apply it first, then
  // install HomeLoom writers so JSON/plain/ansi all land on stderr consistently.
  if (resolved.format === "json") {
    const plain = LogFormat.formats.plain ?? LogFormat(LogFormat.PLAIN);
    Logger.format = LogFormat.PLAIN;
    destination.format = (message) => plain(message);
    destination.write = (_text, message) => {
      writeJsonLog(LogLevel, message, plain);
    };
    destination.add = (message) => {
      destination.write(destination.format(message), message);
    };
    return;
  }

  const formatter = LogFormat(resolved.format);
  Logger.format = resolved.format;
  destination.format = (message) => formatter(message);
  destination.write = (text) => {
    process.stderr.write(`${text}\n`);
  };
  destination.add = (message) => {
    destination.write(destination.format(message), message);
  };
}

function writeJsonLog(
  LogLevel: MatterJsLogLevelLike,
  message: MatterJsLogMessage,
  plain: (diagnostic: unknown, indents?: number) => string,
): void {
  const levelName = (LogLevel[message.level] ?? "info").toUpperCase();
  const body = `${message.prefix ?? ""}${plain(message.values)}`.trim();
  const payload = {
    time: message.now.toISOString(),
    level: levelName,
    msg: body,
    component: "matter-js",
    facility: message.facility,
  };
  process.stderr.write(`${JSON.stringify(payload)}\n`);
}

function normalizeLevel(value: string): MatterJsLogLevelName {
  switch (value.trim().toLowerCase()) {
    case "debug":
      return "debug";
    case "info":
      return "info";
    case "notice":
      return "notice";
    case "warn":
    case "warning":
      return "warn";
    case "error":
      return "error";
    case "fatal":
      return "fatal";
    default:
      throw new Error(
        `unsupported matter.js log level ${value}; expected debug|info|notice|warn|error|fatal`,
      );
  }
}

function normalizeFormat(value: string): MatterJsLogFormatName {
  switch (value.trim().toLowerCase()) {
    case "json":
      return "json";
    case "plain":
    case "text":
      return "plain";
    case "ansi":
      return "ansi";
    default:
      throw new Error(
        `unsupported matter.js log format ${value}; expected json|plain|ansi`,
      );
  }
}
