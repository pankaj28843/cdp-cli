"""Bounded AT-SPI drain for Chrome's exact remote-debugging approval sheet."""

import argparse
import json
import sys
import time

try:
    import pyatspi
except ImportError:
    print("python3-pyatspi is required for Linux remote-debugging approval", file=sys.stderr)
    raise SystemExit(3)


TARGET_SHEET = "Allow remote debugging?"
TARGET_BUTTON = "Allow"
ALLOWED_APPLICATIONS = {
    "Google Chrome",
    "Google Chrome Beta",
    "Google Chrome Canary",
    "Google Chrome Dev",
}
SHEET_ROLES = {
    "alert",
    "dialog",
    "frame",
    "layered pane",
    "panel",
    "window",
}
BUTTON_ROLES = {"button", "push button"}
WINDOW_ROLES = {"dialog", "frame", "window"}
MAX_NODES = 16000
MAX_DEPTH = 30


def text_value(value):
    return value.strip() if isinstance(value, str) else ""


def node_name(node):
    try:
        return text_value(node.name)
    except Exception:
        return ""


def node_description(node):
    try:
        return text_value(node.description)
    except Exception:
        return ""


def role_name(node):
    try:
        return text_value(node.getRoleName()).lower()
    except Exception:
        return ""


def children(node):
    try:
        count = min(max(int(node.childCount), 0), MAX_NODES)
    except Exception:
        return []
    result = []
    for index in range(count):
        try:
            child = node[index]
        except Exception:
            continue
        if child is not None:
            result.append(child)
    return result


def walk(root):
    stack = [(root, 0)]
    visited = 0
    while stack and visited < MAX_NODES:
        node, depth = stack.pop()
        visited += 1
        yield node
        if depth >= MAX_DEPTH:
            continue
        descendants = children(node)
        stack.extend((child, depth + 1) for child in reversed(descendants))


def matching_applications(desktop, process_name):
    matches = []
    for child in children(desktop):
        if role_name(child) == "application" and node_name(child) == process_name:
            matches.append(child)
    return matches


def exact_sheet(node):
    return node_name(node) == TARGET_SHEET and role_name(node) in SHEET_ROLES


def target_button(node):
    if role_name(node) not in BUTTON_ROLES:
        return False
    return node_name(node) == TARGET_BUTTON or node_description(node) == TARGET_BUTTON


def press_target_button(sheet):
    for node in walk(sheet):
        if not target_button(node):
            continue
        try:
            actions = node.queryAction()
            count = min(max(int(actions.nActions), 0), 32)
        except Exception:
            continue
        for index in range(count):
            try:
                action_name = text_value(actions.getName(index)).lower()
            except Exception:
                continue
            if action_name not in {"activate", "click", "press"}:
                continue
            try:
                result = actions.doAction(index)
            except Exception:
                continue
            return result is not False
    return False


def scan(desktop, process_name):
    applications = matching_applications(desktop, process_name)
    windows = 0
    sheets = []
    for application in applications:
        windows += sum(role_name(child) in WINDOW_ROLES for child in children(application))
        for node in walk(application):
            if exact_sheet(node):
                sheets.append(node)
    return windows, sheets


def drain(process_name, press):
    if process_name not in ALLOWED_APPLICATIONS:
        raise ValueError("unsupported Chrome application")
    desktop = pyatspi.Registry.getDesktop(0)
    deadline = time.monotonic() + 12.0
    windows = 0
    sheets = []
    while time.monotonic() < deadline:
        windows, sheets = scan(desktop, process_name)
        if sheets:
            break
        time.sleep(0.2)

    prompt_count_before = len(sheets)
    approved = 0
    if press:
        for _ in range(20):
            windows, sheets = scan(desktop, process_name)
            if not sheets or not press_target_button(sheets[0]):
                break
            approved += 1
            time.sleep(0.2)

    windows, sheets = scan(desktop, process_name)
    return {
        "windows_scanned": windows,
        "prompt_count_before": prompt_count_before,
        "approved_count": approved,
        "prompt_count_after": len(sheets),
    }


def main():
    parser = argparse.ArgumentParser(add_help=False)
    parser.add_argument("--process-name", required=True)
    parser.add_argument("--press", action="store_true")
    args = parser.parse_args()
    report = drain(args.process_name, args.press)
    json.dump(report, sys.stdout, separators=(",", ":"))
    sys.stdout.write("\n")


try:
    main()
except (RuntimeError, TypeError, ValueError):
    raise SystemExit(4)
