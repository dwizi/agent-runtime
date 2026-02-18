#!/usr/bin/env python3
import json
import os
import sys
import urllib.error
import urllib.request


MAX_BODY_BYTES = 128 * 1024


def fail(message: str) -> int:
    sys.stderr.write(message.strip() + "\n")
    return 1


def trim(value) -> str:
    if value is None:
        return ""
    return str(value).strip()


def compact(value: str) -> str:
    text = " ".join(value.strip().split())
    if len(text) <= 1200:
        return text
    return text[:1200] + "..."


def map_string(values, key: str) -> str:
    if not isinstance(values, dict):
        return ""
    return trim(values.get(key))


def nested_map_string(values, parent: str, child: str) -> str:
    if not isinstance(values, dict):
        return ""
    nested = values.get(parent)
    if not isinstance(nested, dict):
        return ""
    return map_string(nested, child)


def first_non_empty(*values: str) -> str:
    for value in values:
        value = trim(value)
        if value:
            return value
    return ""


def map_lookup(values: dict, *keys: str):
    if not isinstance(values, dict):
        return None
    for key in keys:
        if key in values:
            return values[key]
    return None


def parse_error_message(raw: str) -> str:
    text = trim(raw)
    if not text:
        return "no response body"
    try:
        decoded = json.loads(text)
    except json.JSONDecodeError:
        return compact(text)

    error_value = decoded.get("error")
    if isinstance(error_value, str) and trim(error_value):
        return compact(error_value)
    if isinstance(error_value, dict):
        nested = map_string(error_value, "message")
        if nested:
            return compact(nested)

    return compact(first_non_empty(
        map_string(decoded, "message"),
        map_string(decoded, "detail"),
        text,
    ))


def parse_json_or_none(raw: str):
    try:
        return json.loads(raw)
    except json.JSONDecodeError:
        return None


def is_async_action(action_type: str, payload: dict) -> bool:
    if trim(action_type).lower() == "tinyfish_async":
        return True
    if isinstance(payload.get("async"), bool) and payload.get("async"):
        return True
    mode = trim(payload.get("mode")).lower()
    return mode == "async"


def resolve_goal(summary: str, action_target: str, payload: dict) -> str:
    goal = trim(payload.get("goal"))
    if not goal:
        goal = trim(payload.get("task"))
    if not goal:
        goal = trim(summary)
    target = trim(action_target)
    if goal and target and target.lower() not in goal.lower():
        goal = goal + "\nTarget URL: " + target
    return trim(goal)


def resolve_url(action_target: str, payload: dict, request_body: dict) -> str:
    request_url = map_string(request_body, "url")
    if request_url:
        return request_url
    payload_url = trim(payload.get("url"))
    if payload_url:
        return payload_url
    return trim(action_target)


def build_request(summary: str, action_target: str, payload: dict) -> dict:
    request_body = payload.get("request")
    if not isinstance(request_body, dict):
        request_body = {}
    else:
        request_body = dict(request_body)

    goal = map_string(request_body, "goal")
    if not goal:
        goal = resolve_goal(summary, action_target, payload)
        if not goal:
            raise ValueError("tinyfish action requires payload.goal, payload.task, or summary")
        request_body["goal"] = goal

    url = resolve_url(action_target, payload, request_body)
    if not url:
        raise ValueError("tinyfish action requires payload.url, payload.request.url, or action_target")
    request_body["url"] = url

    return request_body


def summarize_response(async_mode: bool, raw: str) -> str:
    text = trim(raw)
    if not text:
        return "tinyfish run queued" if async_mode else "tinyfish run completed"

    decoded = parse_json_or_none(text)
    if not isinstance(decoded, dict):
        return "tinyfish request completed: " + compact(text)

    run_id = first_non_empty(
        map_string(decoded, "run_id"),
        nested_map_string(decoded, "data", "run_id"),
        nested_map_string(decoded, "run", "id"),
    )
    if async_mode:
        if run_id:
            return "tinyfish run queued with run_id " + run_id
        return "tinyfish run queued"

    output = first_non_empty(
        map_string(decoded, "result"),
        map_string(decoded, "output"),
        map_string(decoded, "message"),
        nested_map_string(decoded, "data", "result"),
        nested_map_string(decoded, "data", "output"),
    )
    if output:
        if run_id:
            return "tinyfish run completed (" + run_id + "): " + compact(output)
        return "tinyfish run completed: " + compact(output)
    if run_id:
        return "tinyfish run completed with run_id " + run_id
    return "tinyfish run completed"


def main() -> int:
    raw = sys.stdin.read()
    if not trim(raw):
        return fail("tinyfish plugin expected request JSON on stdin")
    try:
        request = json.loads(raw)
    except json.JSONDecodeError as err:
        return fail(f"tinyfish plugin invalid stdin JSON: {err}")

    approval = request.get("action_approval")
    if not isinstance(approval, dict):
        return fail("tinyfish plugin missing action_approval object")

    payload = map_lookup(approval, "payload", "Payload") or {}
    if not isinstance(payload, dict):
        return fail("tinyfish plugin payload must be a JSON object")

    action_type = trim(map_lookup(approval, "action_type", "ActionType"))
    action_target = trim(map_lookup(approval, "action_target", "ActionTarget"))
    action_summary = trim(map_lookup(approval, "action_summary", "ActionSummary"))

    async_mode = is_async_action(action_type, payload)
    endpoint = "/v1/automation/run-async" if async_mode else "/v1/automation/run"

    try:
        body = build_request(action_summary, action_target, payload)
    except ValueError as err:
        return fail(str(err))

    api_key = trim(os.environ.get("TINYFISH_API_KEY"))
    if not api_key:
        return fail("tinyfish plugin is not configured: missing api key")

    base_url = trim(os.environ.get("TINYFISH_BASE_URL")) or "https://agent.tinyfish.ai"
    base_url = base_url.rstrip("/")
    request_url = base_url + endpoint

    encoded = json.dumps(body).encode("utf-8")
    http_request = urllib.request.Request(
        request_url,
        data=encoded,
        headers={
            "X-API-Key": api_key,
            "Content-Type": "application/json",
        },
        method="POST",
    )

    timeout_value = float(trim(os.environ.get("TINYFISH_TIMEOUT_SEC")) or "90")
    try:
        with urllib.request.urlopen(http_request, timeout=timeout_value) as response:
            response_text = response.read(MAX_BODY_BYTES).decode("utf-8", errors="replace")
            status = int(getattr(response, "status", 0))
    except urllib.error.HTTPError as err:
        error_body = err.read(MAX_BODY_BYTES).decode("utf-8", errors="replace")
        return fail(f"tinyfish request failed: status={err.code} message={parse_error_message(error_body)}")
    except Exception as err:  # noqa: BLE001
        return fail(f"tinyfish request failed: {err}")

    if status < 200 or status >= 300:
        return fail(f"tinyfish request failed: status={status} message={parse_error_message(response_text)}")

    message = summarize_response(async_mode, response_text)
    print(json.dumps({"message": message, "plugin": "tinyfish_agentic_web"}))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
