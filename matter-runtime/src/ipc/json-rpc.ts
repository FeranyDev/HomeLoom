import { randomUUID } from "node:crypto";
import { createConnection, type Socket } from "node:net";

export const JsonRpcErrorCode = {
  parseError: -32_700,
  invalidRequest: -32_600,
  methodNotFound: -32_601,
  invalidParams: -32_602,
  internalError: -32_603,
  overloaded: -32_001,
  handshakeRequired: -32_010,
  protocolVersionMismatch: -32_011,
  targetMismatch: -32_012,
  identityMismatch: -32_013,
  replayRequired: -32_014,
  adapterOperationFailed: -32_020,
} as const;

type JsonRpcId = string | number | null;
type RequestHandler = (params: unknown) => unknown | Promise<unknown>;

interface JsonRpcPeerOptions {
  requestTimeoutMs?: number;
  maxInboundQueue?: number;
  maxOutboundQueue?: number;
  maxFrameBytes?: number;
  idPrefix?: string;
}

interface PendingRequest {
  resolve(value: unknown): void;
  reject(reason: Error): void;
  timer: NodeJS.Timeout;
}

interface OutboundItem {
  readonly frame: string;
  readonly resolve: () => void;
  readonly reject: (error: Error) => void;
}

export interface JsonRpcQueueStats {
  inboundDepth: number;
  inboundMaxDepth: number;
  inboundRejected: number;
  outboundDepth: number;
  outboundMaxDepth: number;
  outboundRejected: number;
}

export class RpcFault extends Error {
  constructor(
    readonly code: number,
    message: string,
    readonly data: unknown = undefined,
  ) {
    super(message);
    this.name = "RpcFault";
  }
}

export class RpcRemoteError extends Error {
  constructor(
    readonly code: number,
    message: string,
    readonly data: unknown = undefined,
  ) {
    super(message);
    this.name = "RpcRemoteError";
  }
}

export class RpcRequestTimeoutError extends Error {
  constructor(
    readonly method: string,
    readonly timeoutMs: number,
  ) {
    super(`JSON-RPC request ${method} timed out after ${timeoutMs}ms`);
    this.name = "RpcRequestTimeoutError";
  }
}

export class RpcDisconnectedError extends Error {
  constructor(message = "JSON-RPC connection closed") {
    super(message);
    this.name = "RpcDisconnectedError";
  }
}

export class RpcBackpressureError extends Error {
  constructor(readonly capacity: number) {
    super(`JSON-RPC outbound queue is full (capacity ${capacity})`);
    this.name = "RpcBackpressureError";
  }
}

class BoundedExecutor {
  readonly #queue: Array<() => Promise<void>> = [];
  #active = false;
  #maxDepth = 0;
  #rejected = 0;

  constructor(readonly capacity: number) {
    if (!Number.isSafeInteger(capacity) || capacity < 1) {
      throw new RangeError("inbound queue capacity must be a positive integer");
    }
  }

  get depth(): number {
    return this.#queue.length + (this.#active ? 1 : 0);
  }

  get maxDepth(): number {
    return this.#maxDepth;
  }

  get rejected(): number {
    return this.#rejected;
  }

  enqueue(job: () => Promise<void>): boolean {
    if (this.depth >= this.capacity) {
      this.#rejected += 1;
      return false;
    }
    this.#queue.push(job);
    this.#maxDepth = Math.max(this.#maxDepth, this.depth);
    this.#pump();
    return true;
  }

  #pump(): void {
    if (this.#active) {
      return;
    }
    const job = this.#queue.shift();
    if (job === undefined) {
      return;
    }
    this.#active = true;
    void job()
      .catch(() => undefined)
      .finally(() => {
        this.#active = false;
        this.#pump();
      });
  }
}

class BoundedSocketWriter {
  readonly #queue: OutboundItem[] = [];
  readonly #socket: Socket;
  #active: OutboundItem | undefined;
  #closedError: Error | undefined;
  #maxDepth = 0;
  #rejected = 0;

  constructor(
    socket: Socket,
    readonly capacity: number,
  ) {
    if (!Number.isSafeInteger(capacity) || capacity < 1) {
      throw new RangeError("outbound queue capacity must be a positive integer");
    }
    this.#socket = socket;
  }

  get depth(): number {
    return this.#queue.length + (this.#active === undefined ? 0 : 1);
  }

  get maxDepth(): number {
    return this.#maxDepth;
  }

  get rejected(): number {
    return this.#rejected;
  }

  send(message: unknown): Promise<void> {
    if (this.#closedError !== undefined) {
      return Promise.reject(this.#closedError);
    }
    if (this.depth >= this.capacity) {
      this.#rejected += 1;
      return Promise.reject(new RpcBackpressureError(this.capacity));
    }
    const frame = `${JSON.stringify(message)}\n`;
    return new Promise<void>((resolve, reject) => {
      this.#queue.push({ frame, resolve, reject });
      this.#maxDepth = Math.max(this.#maxDepth, this.depth);
      this.#pump();
    });
  }

  close(error: Error): void {
    if (this.#closedError !== undefined) {
      return;
    }
    this.#closedError = error;
    const active = this.#active;
    this.#active = undefined;
    active?.reject(error);
    for (const item of this.#queue.splice(0)) {
      item.reject(error);
    }
  }

  #pump(): void {
    if (this.#active !== undefined || this.#closedError !== undefined) {
      return;
    }
    const item = this.#queue.shift();
    if (item === undefined) {
      return;
    }
    this.#active = item;
    try {
      this.#socket.write(item.frame, (error?: Error | null) => {
        if (this.#active !== item) {
          return;
        }
        this.#active = undefined;
        if (error !== undefined && error !== null) {
          item.reject(error);
        } else {
          item.resolve();
        }
        this.#pump();
      });
    } catch (error) {
      this.#active = undefined;
      item.reject(asError(error));
      this.#pump();
    }
  }
}

/**
 * Newline-delimited JSON-RPC 2.0 peer. Both endpoints may register handlers and
 * issue requests, which is required for Matter controller writes flowing back
 * to the Go host on the same Unix socket.
 */
export class JsonRpcPeer {
  readonly #handlers = new Map<string, RequestHandler>();
  readonly #pending = new Map<string | number, PendingRequest>();
  readonly #closeListeners = new Set<(error: Error) => void>();
  readonly #writer: BoundedSocketWriter;
  readonly #executor: BoundedExecutor;
  readonly #requestTimeoutMs: number;
  readonly #maxFrameBytes: number;
  readonly #idPrefix: string;
  #nextId = 0;
  #readBuffer = "";
  #closedError: Error | undefined;

  constructor(
    readonly socket: Socket,
    options: JsonRpcPeerOptions = {},
  ) {
    this.#requestTimeoutMs = options.requestTimeoutMs ?? 5_000;
    this.#maxFrameBytes = options.maxFrameBytes ?? 1024 * 1024;
    this.#idPrefix = options.idPrefix ?? randomUUID();
    this.#writer = new BoundedSocketWriter(socket, options.maxOutboundQueue ?? 128);
    this.#executor = new BoundedExecutor(options.maxInboundQueue ?? 128);

    socket.setEncoding("utf8");
    socket.on("data", (chunk: string) => this.#onData(chunk));
    socket.on("error", (error) => this.#finishClose(error));
    socket.on("close", () => this.#finishClose(new RpcDisconnectedError()));
  }

  get closed(): boolean {
    return this.#closedError !== undefined;
  }

  get queueStats(): JsonRpcQueueStats {
    return {
      inboundDepth: this.#executor.depth,
      inboundMaxDepth: this.#executor.maxDepth,
      inboundRejected: this.#executor.rejected,
      outboundDepth: this.#writer.depth,
      outboundMaxDepth: this.#writer.maxDepth,
      outboundRejected: this.#writer.rejected,
    };
  }

  register(method: string, handler: RequestHandler): void {
    if (method.trim() === "") {
      throw new TypeError("JSON-RPC method must be non-empty");
    }
    if (this.#handlers.has(method)) {
      throw new Error(`JSON-RPC handler already registered for ${method}`);
    }
    this.#handlers.set(method, handler);
  }

  request<T = unknown>(method: string, params: unknown, timeoutMs = this.#requestTimeoutMs): Promise<T> {
    if (this.#closedError !== undefined) {
      return Promise.reject(this.#closedError);
    }
    const id = `${this.#idPrefix}:${++this.#nextId}`;
    return new Promise<T>((resolve, reject) => {
      const timer = setTimeout(() => {
        this.#pending.delete(id);
        reject(new RpcRequestTimeoutError(method, timeoutMs));
      }, timeoutMs);
      timer.unref();
      this.#pending.set(id, {
        resolve: (value) => resolve(value as T),
        reject,
        timer,
      });
      void this.#writer
        .send({ jsonrpc: "2.0", id, method, params })
        .catch((error: unknown) => this.#rejectPending(id, asError(error)));
    });
  }

  notify(method: string, params: unknown): Promise<void> {
    return this.#writer.send({ jsonrpc: "2.0", method, params });
  }

  onClose(listener: (error: Error) => void): () => void {
    if (this.#closedError !== undefined) {
      listener(this.#closedError);
      return () => undefined;
    }
    this.#closeListeners.add(listener);
    return () => this.#closeListeners.delete(listener);
  }

  close(error = new RpcDisconnectedError("JSON-RPC peer closed locally")): void {
    this.#finishClose(error);
    this.socket.destroy();
  }

  #onData(chunk: string): void {
    if (this.#closedError !== undefined) {
      return;
    }
    this.#readBuffer += chunk;
    if (Buffer.byteLength(this.#readBuffer) > this.#maxFrameBytes && !this.#readBuffer.includes("\n")) {
      this.close(new RpcFault(JsonRpcErrorCode.invalidRequest, "JSON-RPC frame exceeds maximum size"));
      return;
    }
    while (true) {
      const newline = this.#readBuffer.indexOf("\n");
      if (newline < 0) {
        return;
      }
      const frame = this.#readBuffer.slice(0, newline).replace(/\r$/, "");
      this.#readBuffer = this.#readBuffer.slice(newline + 1);
      if (frame.trim() === "") {
        continue;
      }
      if (Buffer.byteLength(frame) > this.#maxFrameBytes) {
        this.close(new RpcFault(JsonRpcErrorCode.invalidRequest, "JSON-RPC frame exceeds maximum size"));
        return;
      }
      this.#onFrame(frame);
    }
  }

  #onFrame(frame: string): void {
    let message: unknown;
    try {
      message = JSON.parse(frame) as unknown;
    } catch {
      void this.#sendError(null, new RpcFault(JsonRpcErrorCode.parseError, "invalid JSON"));
      return;
    }
    if (!isRecord(message) || message.jsonrpc !== "2.0") {
      void this.#sendError(extractId(message), new RpcFault(JsonRpcErrorCode.invalidRequest, "invalid JSON-RPC message"));
      return;
    }
    if (typeof message.method === "string") {
      const hasId = typeof message.id === "string" || typeof message.id === "number";
      this.#onIncomingCall(message.method, message.params, hasId ? message.id as string | number : undefined);
      return;
    }
    if ((typeof message.id === "string" || typeof message.id === "number") &&
        (Object.hasOwn(message, "result") || Object.hasOwn(message, "error"))) {
      this.#onResponse(message.id, message);
      return;
    }
    void this.#sendError(extractId(message), new RpcFault(JsonRpcErrorCode.invalidRequest, "invalid JSON-RPC message"));
  }

  #onIncomingCall(method: string, params: unknown, id: string | number | undefined): void {
    const accepted = this.#executor.enqueue(async () => {
      const handler = this.#handlers.get(method);
      if (handler === undefined) {
        if (id !== undefined) {
          await this.#sendError(id, new RpcFault(JsonRpcErrorCode.methodNotFound, `method not found: ${method}`));
        }
        return;
      }
      try {
        const result = await handler(params);
        if (id !== undefined) {
          await this.#writer.send({ jsonrpc: "2.0", id, result: result ?? null });
        }
      } catch (error) {
        if (id !== undefined) {
          await this.#sendError(id, normalizeFault(error));
        }
      }
    });
    if (!accepted && id !== undefined) {
      void this.#sendError(id, new RpcFault(JsonRpcErrorCode.overloaded, "inbound request queue is full"));
    }
  }

  #onResponse(id: string | number, message: Record<string, unknown>): void {
    const pending = this.#pending.get(id);
    if (pending === undefined) {
      return;
    }
    this.#pending.delete(id);
    clearTimeout(pending.timer);
    if (isRecord(message.error)) {
      const code = typeof message.error.code === "number" ? message.error.code : JsonRpcErrorCode.internalError;
      const text = typeof message.error.message === "string" ? message.error.message : "remote JSON-RPC error";
      pending.reject(new RpcRemoteError(code, text, message.error.data));
      return;
    }
    pending.resolve(message.result);
  }

  async #sendError(id: JsonRpcId, fault: RpcFault): Promise<void> {
    const error: Record<string, unknown> = {
      code: fault.code,
      message: fault.message,
    };
    if (fault.data !== undefined) {
      error.data = fault.data;
    }
    try {
      await this.#writer.send({ jsonrpc: "2.0", id, error });
    } catch (sendError) {
      this.close(asError(sendError));
    }
  }

  #rejectPending(id: string | number, error: Error): void {
    const pending = this.#pending.get(id);
    if (pending === undefined) {
      return;
    }
    this.#pending.delete(id);
    clearTimeout(pending.timer);
    pending.reject(error);
  }

  #finishClose(error: Error): void {
    if (this.#closedError !== undefined) {
      return;
    }
    this.#closedError = error;
    this.#writer.close(error);
    for (const [id, pending] of this.#pending) {
      clearTimeout(pending.timer);
      pending.reject(error);
      this.#pending.delete(id);
    }
    for (const listener of this.#closeListeners) {
      listener(error);
    }
    this.#closeListeners.clear();
  }
}

export async function connectJsonRpcPeer(
  socketPath: string,
  options: JsonRpcPeerOptions = {},
): Promise<JsonRpcPeer> {
  const socket = createConnection(socketPath);
  await new Promise<void>((resolve, reject) => {
    const onConnect = (): void => {
      socket.off("error", onError);
      resolve();
    };
    const onError = (error: Error): void => {
      socket.off("connect", onConnect);
      reject(error);
    };
    socket.once("connect", onConnect);
    socket.once("error", onError);
  });
  return new JsonRpcPeer(socket, options);
}

function normalizeFault(error: unknown): RpcFault {
  if (error instanceof RpcFault) {
    return error;
  }
  return new RpcFault(JsonRpcErrorCode.internalError, asError(error).message);
}

function extractId(value: unknown): JsonRpcId {
  if (isRecord(value) && (typeof value.id === "string" || typeof value.id === "number" || value.id === null)) {
    return value.id;
  }
  return null;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function asError(error: unknown): Error {
  return error instanceof Error ? error : new Error(String(error));
}
