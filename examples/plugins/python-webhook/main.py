#!/usr/bin/env python3
"""Пример плагина mKey на Python (docs/plugins.md) — без SDK, только стандартная библиотека.

Действие http_request отправляет запрос на адрес; триггер http_webhook запускает событие,
когда на http://127.0.0.1:<порт><путь> приходит запрос (тело запроса — в переменной body).
Протокол: JSON-RPC 2.0, одно сообщение на строку; stdout — только протокол, журнал — stderr.
"""

import json
import sys
import threading
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

# Запись в stdout — под замком: ответы и уведомления идут из разных потоков.
out_lock = threading.Lock()


def send(msg):
    """Отправляет одно сообщение протокола одной строкой."""
    with out_lock:
        sys.stdout.write(json.dumps(msg, ensure_ascii=False) + "\n")
        sys.stdout.flush()


def log(text):
    """Пишет строку в журнал плагина (stderr)."""
    print(text, file=sys.stderr, flush=True)


TYPES = {
    "actions": [{
        "id": "http_request",
        "name": {"ru": "HTTP-запрос", "en": "HTTP request"},
        "description": {"ru": "Отправить запрос на адрес в интернете", "en": "Send a request to a web address"},
        "category": "system",
        "params_schema": {
            "type": "object", "required": ["url"],
            "properties": {
                "url": {"type": "string"},
                "method": {"enum": ["GET", "POST"], "default": "GET"},
                "body": {"type": "string"},
            },
        },
    }],
    "conditions": [],
    "triggers": [{
        "id": "http_webhook",
        "name": {"ru": "Вебхук", "en": "Webhook"},
        "description": {"ru": "Событие по запросу на адрес http://127.0.0.1:<порт><путь>", "en": "Fires on a request to http://127.0.0.1:<port><path>"},
        "params_schema": {
            "type": "object",
            "properties": {
                "port": {"type": "integer", "default": 8090},
                "path": {"type": "string", "default": "/hook"},
            },
        },
    }],
}

# Взведённые вебхуки: порт → {путь → handle}; серверы по портам.
hooks = {}
servers = {}
hooks_lock = threading.Lock()


def make_handler(port):
    """Создаёт обработчик HTTP-запросов для порта: путь → срабатывание триггера."""

    class Handler(BaseHTTPRequestHandler):
        def do_any(self):
            path = self.path.split("?", 1)[0]
            with hooks_lock:
                handle = hooks.get(port, {}).get(path)
            if handle is None:
                self.send_response(404)
                self.end_headers()
                return
            size = int(self.headers.get("Content-Length") or 0)
            body = self.rfile.read(size).decode("utf-8", "replace") if size else ""
            send({"jsonrpc": "2.0", "method": "trigger.fire", "params": {"handle": handle, "vars": {"body": body}}})
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b"ok\n")

        do_GET = do_any
        do_POST = do_any

        def log_message(self, fmt, *args):
            log("webhook: " + fmt % args)

    return Handler


def arm(params, handle):
    """Взводит вебхук: при необходимости запускает сервер на порту (только 127.0.0.1)."""
    port = int(params.get("port") or 8090)
    path = params.get("path") or "/hook"
    with hooks_lock:
        if port not in servers:
            server = ThreadingHTTPServer(("127.0.0.1", port), make_handler(port))
            threading.Thread(target=server.serve_forever, daemon=True).start()
            servers[port] = server
        hooks.setdefault(port, {})[path] = handle


def disarm(handle):
    """Снимает вебхук; сервер без вебхуков останавливается."""
    with hooks_lock:
        for port, paths in list(hooks.items()):
            for path, h in list(paths.items()):
                if h == handle:
                    del paths[path]
            if not paths:
                del hooks[port]
                servers.pop(port).shutdown()


def http_request(params):
    """Выполняет действие «HTTP-запрос»."""
    url = params.get("url") or ""
    if not url.startswith(("http://", "https://")):
        raise ValueError("адрес должен начинаться с http:// или https://")
    data = params.get("body")
    req = urllib.request.Request(url, data=data.encode() if data else None, method=params.get("method") or "GET")
    with urllib.request.urlopen(req, timeout=10) as resp:
        log("http_request %s → %s" % (url, resp.status))


def handle(msg):
    """Выполняет один запрос mKey и возвращает result (или бросает исключение)."""
    method, p = msg.get("method"), msg.get("params") or {}
    params = p.get("params") or {}
    if method == "initialize":
        return TYPES
    if method in ("ping", "shutdown"):
        return {}
    if method == "action.validate":
        url = params.get("url") or ""
        if not url.startswith(("http://", "https://")):
            raise ValueError("адрес должен начинаться с http:// или https://")
        return {}
    if method == "action.run":
        http_request(params)
        return {}
    if method == "trigger.arm":
        arm(params, p["handle"])
        return {}
    if method == "trigger.disarm":
        disarm(p["handle"])
        return {}
    raise LookupError(method)


def serve(msg):
    """Отвечает на запрос (в своём потоке: действие может идти долго)."""
    try:
        result = handle(msg)
        send({"jsonrpc": "2.0", "id": msg["id"], "result": result})
    except LookupError as e:
        send({"jsonrpc": "2.0", "id": msg["id"], "error": {"code": -32601, "message": "method %s not found" % e}})
    except Exception as e:  # noqa: BLE001 — любая ошибка — понятный ответ mKey
        send({"jsonrpc": "2.0", "id": msg["id"], "error": {"code": -32000, "message": str(e)}})
    if msg.get("method") == "shutdown":
        sys.exit(0)


def main():
    """Читает запросы mKey построчно до конца stdin или команды shutdown."""
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        msg = json.loads(line)
        if "id" not in msg or "method" not in msg:
            continue  # уведомления ($/cancelRequest) и ответы этому плагину не нужны
        if msg["method"] == "shutdown":
            serve(msg)
        threading.Thread(target=serve, args=(msg,), daemon=True).start()


if __name__ == "__main__":
    main()
