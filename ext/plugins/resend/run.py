#!/usr/bin/env python3
import json
import os
import sys
import urllib.error
import urllib.request


def fail(message: str) -> int:
    sys.stderr.write(message.strip() + "\n")
    return 1


def str_or_empty(value) -> str:
    if value is None:
        return ""
    return str(value).strip()


def normalize_recipients(value):
    if value is None:
        return []
    if isinstance(value, str):
        return [item.strip() for item in value.split(",") if item.strip()]
    if isinstance(value, list):
        return [str_or_empty(item) for item in value if str_or_empty(item)]
    return []


def map_lookup(values: dict, *keys: str):
    if not isinstance(values, dict):
        return None
    for key in keys:
        if key in values:
            return values[key]
    return None


def parse_message(response_body: str) -> str:
    if not response_body:
        return ""
    try:
        decoded = json.loads(response_body)
    except json.JSONDecodeError:
        return response_body.strip()
    if isinstance(decoded, dict):
        for key in ("message", "error", "detail"):
            value = decoded.get(key)
            if isinstance(value, str) and value.strip():
                return value.strip()
            if isinstance(value, dict):
                nested = value.get("message")
                if isinstance(nested, str) and nested.strip():
                    return nested.strip()
    return response_body.strip()


def main() -> int:
    raw = sys.stdin.read()
    if not raw.strip():
        return fail("resend plugin expected request JSON on stdin")

    try:
        request = json.loads(raw)
    except json.JSONDecodeError as err:
        return fail(f"resend plugin invalid stdin JSON: {err}")

    approval = request.get("action_approval")
    if not isinstance(approval, dict):
        return fail("resend plugin missing action_approval object")

    payload = map_lookup(approval, "payload", "Payload") or {}
    if not isinstance(payload, dict):
        return fail("resend plugin payload must be a JSON object")

    action_target = str_or_empty(map_lookup(approval, "action_target", "ActionTarget"))
    action_summary = str_or_empty(map_lookup(approval, "action_summary", "ActionSummary"))

    to_values = normalize_recipients(payload.get("to"))
    if not to_values and action_target:
        to_values = normalize_recipients(action_target)
    if not to_values:
        return fail("resend_email requires payload.to or action target recipient email")

    from_value = str_or_empty(payload.get("from")) or str_or_empty(os.environ.get("RESEND_FROM"))
    if not from_value:
        return fail("resend_email requires payload.from or RESEND_FROM env var")

    subject = str_or_empty(payload.get("subject")) or action_summary or "Message from agent-runtime"
    text = str_or_empty(payload.get("text"))
    html = str_or_empty(payload.get("html"))
    if not text and not html:
        text = action_summary
    if not text and not html:
        return fail("resend_email requires payload.text or payload.html (or non-empty summary)")

    cc_values = normalize_recipients(payload.get("cc"))
    bcc_values = normalize_recipients(payload.get("bcc"))

    body = {
        "from": from_value,
        "to": to_values,
        "subject": subject,
    }
    if text:
        body["text"] = text
    if html:
        body["html"] = html
    if cc_values:
        body["cc"] = cc_values
    if bcc_values:
        body["bcc"] = bcc_values

    reply_to = payload.get("reply_to")
    if isinstance(reply_to, str) and reply_to.strip():
        body["reply_to"] = reply_to.strip()
    elif isinstance(reply_to, list):
        normalized = normalize_recipients(reply_to)
        if normalized:
            body["reply_to"] = normalized

    tags = payload.get("tags")
    if isinstance(tags, list):
        body["tags"] = tags

    headers = payload.get("headers")
    if isinstance(headers, dict):
        body["headers"] = headers

    api_key = str_or_empty(os.environ.get("RESEND_API_KEY"))
    if not api_key:
        return fail("resend plugin missing RESEND_API_KEY")

    base = str_or_empty(os.environ.get("RESEND_API_BASE")) or "https://api.resend.com"
    endpoint = base.rstrip("/") + "/emails"
    timeout_sec = float(str_or_empty(os.environ.get("RESEND_TIMEOUT_SEC")) or "30")

    encoded = json.dumps(body).encode("utf-8")
    req = urllib.request.Request(
        endpoint,
        data=encoded,
        headers={
            "Authorization": f"Bearer {api_key}",
            "Content-Type": "application/json",
            "User-Agent": "curl/8.7.1",
        },
        method="POST",
    )

    try:
        with urllib.request.urlopen(req, timeout=timeout_sec) as response:
            response_text = response.read().decode("utf-8", errors="replace")
            status_code = int(getattr(response, "status", 0))
    except urllib.error.HTTPError as err:
        error_body = err.read().decode("utf-8", errors="replace")
        message = parse_message(error_body) or f"HTTP {err.code}"
        return fail(f"resend request failed: status={err.code} message={message}")
    except Exception as err:  # noqa: BLE001
        return fail(f"resend request failed: {err}")

    if status_code < 200 or status_code >= 300:
        return fail(f"resend request failed: unexpected status {status_code}")

    email_id = ""
    if response_text:
        try:
            parsed = json.loads(response_text)
            if isinstance(parsed, dict):
                email_id = str_or_empty(parsed.get("id"))
        except json.JSONDecodeError:
            pass

    recipients = ", ".join(to_values)
    message = f"resend email sent to {recipients}"
    if email_id:
        message += f" (id: {email_id})"
    print(json.dumps({"message": message, "plugin": "external_resend_email"}))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
