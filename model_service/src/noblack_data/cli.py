from __future__ import annotations

import argparse
import json
import logging
from pathlib import Path

from .pipeline import BuildConfig, build_dataset


def make_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="noblack-data",
        description="Prepare leakage-safe Chinese sexual-content and homophone-evasion datasets.",
    )
    subparsers = parser.add_subparsers(dest="command", required=True)
    build = subparsers.add_parser("build", help="Build processed JSONL datasets")
    build.add_argument("--raw-dir", type=Path, default=Path("data/raw"))
    build.add_argument("--output-dir", type=Path, default=Path("data/processed"))
    build.add_argument("--seed", type=int, default=20260714)
    build.add_argument("--train-ratio", type=float, default=0.8)
    build.add_argument("--dev-ratio", type=float, default=0.1)
    build.add_argument("--max-per-template", type=int, default=3)
    build.add_argument("--near-duplicate-hamming", type=int, default=3)
    build.add_argument("--near-duplicate-jaccard", type=float, default=0.88)
    build.add_argument("--augmentations-per-row", type=int, default=3)
    build.add_argument("--log-level", choices=("DEBUG", "INFO", "WARNING", "ERROR"), default="INFO")
    return parser


def main(argv: list[str] | None = None) -> int:
    args = make_parser().parse_args(argv)
    logging.basicConfig(level=getattr(logging, args.log_level), format="%(levelname)s %(name)s %(message)s")
    if args.command == "build":
        if not 0 < args.train_ratio < 1 or not 0 <= args.dev_ratio < 1:
            raise SystemExit("split ratios must be between 0 and 1")
        if args.train_ratio + args.dev_ratio >= 1:
            raise SystemExit("train_ratio + dev_ratio must be less than 1")
        config = BuildConfig(
            seed=args.seed,
            train_ratio=args.train_ratio,
            dev_ratio=args.dev_ratio,
            max_per_template=args.max_per_template,
            near_duplicate_hamming=args.near_duplicate_hamming,
            near_duplicate_jaccard=args.near_duplicate_jaccard,
            augmentations_per_row=args.augmentations_per_row,
        )
        manifest = build_dataset(args.raw_dir.resolve(), args.output_dir.resolve(), config)
        summary = {
            "output_dir": str(args.output_dir.resolve()),
            "output_counts": manifest["output_counts"],
            "validation": manifest["validation"],
        }
        print(json.dumps(summary, ensure_ascii=False, indent=2))
        return 0
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
