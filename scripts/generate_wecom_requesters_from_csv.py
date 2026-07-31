#!/usr/bin/env python3
"""Generate a flat-scope WeCom requester policy from a mapped CSV export."""

from __future__ import annotations

import argparse
import csv
import json
import re
from pathlib import Path
from typing import Iterable


CAPABILITIES = [
    "qdm.cas.token",
    "qdm.cmr.query",
    "qdm.indicators.query",
    "qdm.sql.select",
]

STORE_ALL_MAPPING = "all=门店维度组/全部"
WAREHOUSE_ALL_MAPPING = "all=仓维度组/全部"
CATEGORY_ALL_MAPPING = "all=商品维度组/全部"


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Generate a flat-scope WeCom requester permission JSON from a mapped CSV export."
    )
    parser.add_argument("csv_path", type=Path)
    parser.add_argument("output_path", type=Path)
    parser.add_argument("--bot-id", required=True)
    parser.add_argument(
        "--report",
        type=Path,
        help="Optional JSON report describing generated users and dimensions.",
    )
    return parser.parse_args()


def unique(values: Iterable[str]) -> list[str]:
    result: list[str] = []
    seen: set[str] = set()
    for value in values:
        value = value.strip()
        if value and value not in seen:
            seen.add(value)
            result.append(value)
    return result


def typed_mapping_ids(mapping: str, dimension_type: str) -> list[str]:
    values: list[str] = []
    for item in re.split(r"\s+;\s+", mapping.strip()):
        if not item or "=" not in item:
            continue
        value, description = item.split("=", 1)
        if f"({dimension_type})" in description:
            values.append(value)
    return unique(values)


def natural_id_key(value: str) -> tuple[int, int | str]:
    if value.isdigit():
        return (0, int(value))
    return (1, value)


def load_rows(path: Path) -> list[dict[str, str]]:
    with path.open("r", encoding="utf-8-sig", newline="") as source:
        rows = list(csv.DictReader(source))
    required = {"帐号", "姓名", "门店Mapping", "仓Mapping", "商品Mapping"}
    missing = sorted(required.difference(rows[0].keys() if rows else set()))
    if missing:
        raise ValueError(f"CSV is missing required columns: {', '.join(missing)}")
    return rows


def all_ids(rows: list[dict[str, str]], column: str, dimension_type: str) -> list[str]:
    return sorted(
        {
            value
            for row in rows
            for value in typed_mapping_ids(row.get(column, ""), dimension_type)
        },
        key=natural_id_key,
    )


def mapped_ids(mapping: str, all_mapping: str, known_ids: list[str], dimension_type: str) -> list[str]:
    mapping = mapping.strip()
    if mapping == all_mapping:
        return list(known_ids)
    return typed_mapping_ids(mapping, dimension_type)


def build_document(rows: list[dict[str, str]], bot_id: str) -> tuple[dict[str, object], dict[str, object]]:
    all_manage_area_ids = all_ids(rows, "门店Mapping", "manageAreaId")
    all_dc_manage_area_ids = all_ids(rows, "仓Mapping", "dcManageAreaId")
    all_category_level1_ids = all_ids(rows, "商品Mapping", "categoryLevel1Id")

    users: list[dict[str, object]] = []
    seen_user_ids: set[str] = set()
    skipped_rows: list[dict[str, str]] = []
    disabled_reasons: dict[str, int] = {}
    warehouse_scope_users = 0
    enabled_warehouse_scope_users = 0

    for row_number, row in enumerate(rows, start=2):
        user_id = row.get("帐号", "").strip()
        display_name = row.get("姓名", "").strip()
        if not user_id:
            skipped_rows.append({"row": str(row_number), "reason": "missingUserId"})
            continue
        if user_id in seen_user_ids:
            skipped_rows.append(
                {"row": str(row_number), "reason": "duplicateUserId", "userId": user_id}
            )
            continue
        seen_user_ids.add(user_id)

        manage_area_ids = mapped_ids(
            row.get("门店Mapping", ""),
            STORE_ALL_MAPPING,
            all_manage_area_ids,
            "manageAreaId",
        )
        dc_manage_area_ids = mapped_ids(
            row.get("仓Mapping", ""),
            WAREHOUSE_ALL_MAPPING,
            all_dc_manage_area_ids,
            "dcManageAreaId",
        )
        category_level1_ids = mapped_ids(
            row.get("商品Mapping", ""),
            CATEGORY_ALL_MAPPING,
            all_category_level1_ids,
            "categoryLevel1Id",
        )

        if dc_manage_area_ids:
            warehouse_scope_users += 1

        reasons: list[str] = []
        if not manage_area_ids and not dc_manage_area_ids:
            reasons.append("noManageAreaOrDCManageArea")
        if not category_level1_ids:
            reasons.append("noCategoryLevel1")
        enabled = not reasons
        if enabled and dc_manage_area_ids:
            enabled_warehouse_scope_users += 1
        for reason in reasons:
            disabled_reasons[reason] = disabled_reasons.get(reason, 0) + 1

        users.append(
            {
                "userId": user_id,
                "displayName": display_name,
                "enabled": enabled,
                "capabilities": list(CAPABILITIES) if enabled else [],
                "scope": {
                    "manageAreaIds": manage_area_ids,
                    "dcManageAreaIds": dc_manage_area_ids,
                    "categoryLevel1Ids": category_level1_ids,
                },
            }
        )

    document: dict[str, object] = {
        "version": 1,
        "botId": bot_id.strip(),
        "users": users,
    }
    report: dict[str, object] = {
        "csvRows": len(rows),
        "outputUsers": len(users),
        "enabledUsers": sum(bool(user["enabled"]) for user in users),
        "disabledUsers": sum(not bool(user["enabled"]) for user in users),
        "warehouseManagementScopeUsers": warehouse_scope_users,
        "enabledWarehouseManagementScopeUsers": enabled_warehouse_scope_users,
        "allManageAreaIds": all_manage_area_ids,
        "allDCManageAreaIds": all_dc_manage_area_ids,
        "allCategoryLevel1Ids": all_category_level1_ids,
        "disabledReasonCounts": disabled_reasons,
        "skippedRows": skipped_rows,
        "schema": {
            "scopeKeys": [
                "manageAreaIds",
                "dcManageAreaIds",
                "categoryLevel1Ids",
            ]
        },
    }
    return document, report


def write_json(path: Path, value: object, mode: int) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(
        json.dumps(value, ensure_ascii=False, indent=2) + "\n",
        encoding="utf-8",
    )
    path.chmod(mode)


def main() -> None:
    args = parse_args()
    rows = load_rows(args.csv_path)
    document, report = build_document(rows, args.bot_id)
    if not document["botId"]:
        raise ValueError("--bot-id must not be empty")
    write_json(args.output_path, document, 0o600)
    if args.report:
        write_json(args.report, report, 0o600)
    print(
        f"generated {args.output_path}: "
        f"{report['outputUsers']} users, {report['enabledUsers']} enabled, "
        f"{report['disabledUsers']} disabled, "
        f"{report['warehouseManagementScopeUsers']} with dcManageAreaIds"
    )


if __name__ == "__main__":
    main()
