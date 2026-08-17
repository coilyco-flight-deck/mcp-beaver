#!/usr/bin/env python3
"""Post a CI-failed-on-main notice to Telegram. Best effort, never fatal."""
import os
import sys
import urllib.error
import urllib.parse
import urllib.request

FIELDS = ("REPO", "WORKFLOW", "JOB", "REF", "SHA", "RUN_URL")


def build_message() -> str:
    lines = ["CI failed on main"]
    lines += [f"{name.lower()}: {os.environ.get(name, '')}" for name in FIELDS]
    return "\n".join(lines)


def main() -> int:
    bot_token = os.environ.get("BOT_TOKEN", "")
    chat_id = os.environ.get("CHAT_ID", "")
    if not bot_token or not chat_id:
        print("telegram alert missing required secret", file=sys.stderr)
        return 2

    payload = urllib.parse.urlencode(
        {
            "chat_id": chat_id,
            "text": build_message(),
            "disable_web_page_preview": "true",
        }
    ).encode("utf-8")
    api_base = os.environ.get("API_BASE", "https://api.telegram.org").rstrip("/")
    request = urllib.request.Request(
        f"{api_base}/bot{bot_token}/sendMessage", data=payload, method="POST"
    )
    proxy_url = os.environ.get("FORGEJO_EGRESS_PROXY", "").strip()
    handler = urllib.request.ProxyHandler({"https": proxy_url} if proxy_url else {})
    try:
        with urllib.request.build_opener(handler).open(request, timeout=15) as resp:
            resp.read()
    except urllib.error.URLError as exc:
        print(f"telegram alert failed ({type(exc).__name__})", file=sys.stderr)
        return 1
    return 0


raise SystemExit(main())
