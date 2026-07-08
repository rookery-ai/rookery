"""Composio tool-slug discovery CLI — platform-provided, do not edit.

Costs exactly ONE run_script call (vs. writing your own discovery script first). Use this
before hardcoding any Composio tool_slug, for ANY connected service — there is no
hardcoded slug list; this always asks Composio live.

Usage:
    python3 tools/composio_discover.py --toolkit gmail --query "create a draft"
    python3 tools/composio_discover.py --toolkit notion --query "search page" --limit 10

Prints a JSON array of {"slug", "name", "description"} to stdout. Read the description of
each candidate and pick the one that actually matches what the user asked for — pay close
attention to DRAFT/CREATE vs SEND/PUBLISH, they are usually different actions.
"""
import argparse
import json
import sys

from composio_helper import list_tools, ComposioError


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--toolkit", required=True, help="toolkit slug, e.g. gmail, notion, slack")
    p.add_argument("--query", default=None, help="plain-language description of the action")
    p.add_argument("--limit", type=int, default=50)
    args = p.parse_args()

    try:
        tools = list_tools(args.toolkit, query=args.query, limit=args.limit)
    except ComposioError as e:
        print(json.dumps({"error": str(e)}))
        sys.exit(1)

    if not tools:
        print(json.dumps({"error": f"No tools found for toolkit '{args.toolkit}'"
                                    + (f" matching '{args.query}'" if args.query else "")
                                    + ". Double-check the toolkit slug is correct."}))
        sys.exit(1)

    print(json.dumps(tools, indent=2))


if __name__ == "__main__":
    main()
