from __future__ import annotations

import argparse
import json
import logging
import sys
from pathlib import Path

from .inference import SafetyPredictor, predict_texts
from .training import TrainConfig, evaluate_checkpoint, train


def make_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="noblack-model")
    subparsers = parser.add_subparsers(dest="command", required=True)

    train_parser = subparsers.add_parser("train", help="Train a dual-branch classifier")
    train_parser.add_argument("--processed-dir", type=Path, default=Path("data/processed"))
    train_parser.add_argument("--output-dir", type=Path, default=Path("artifacts/models/lite-baseline"))
    train_parser.add_argument("--encoder-type", choices=("lite", "pretrained"), default="lite")
    train_parser.add_argument("--model-name", default="hfl/chinese-macbert-base")
    train_parser.add_argument("--include-augmented", action=argparse.BooleanOptionalAction, default=True)
    train_parser.add_argument("--production-data-dir", type=Path)
    train_parser.add_argument("--production-negative-repeat", type=int, default=1)
    train_parser.add_argument("--max-length", type=int, default=128)
    train_parser.add_argument("--batch-size", type=int, default=32)
    train_parser.add_argument("--epochs", type=int, default=3)
    train_parser.add_argument("--learning-rate", type=float, default=2e-4)
    train_parser.add_argument("--encoder-learning-rate", type=float, default=2e-5)
    train_parser.add_argument("--weight-decay", type=float, default=0.01)
    train_parser.add_argument("--auxiliary-weight", type=float, default=0.1)
    train_parser.add_argument("--consistency-weight", type=float, default=0.2)
    train_parser.add_argument("--representation-weight", type=float, default=0.1)
    train_parser.add_argument("--gradient-accumulation", type=int, default=1)
    train_parser.add_argument("--seed", type=int, default=20260714)
    train_parser.add_argument("--amp", action=argparse.BooleanOptionalAction, default=True)
    train_parser.add_argument("--limit-train", type=int)
    train_parser.add_argument("--limit-dev", type=int)
    train_parser.add_argument("--target-block-precision", type=float, default=0.9)
    train_parser.add_argument("--target-pass-recall", type=float, default=0.98)
    train_parser.add_argument("--log-level", choices=("DEBUG", "INFO", "WARNING", "ERROR"), default="INFO")

    evaluate_parser = subparsers.add_parser("evaluate", help="Evaluate a checkpoint without emitting raw samples")
    evaluate_parser.add_argument("--checkpoint-dir", type=Path, required=True)
    evaluate_parser.add_argument("--data-path", type=Path, required=True)
    evaluate_parser.add_argument("--output-path", type=Path)
    evaluate_parser.add_argument("--batch-size", type=int, default=64)

    interactive_parser = subparsers.add_parser("interactive", help="Load a model once and test texts interactively")
    interactive_parser.add_argument("--checkpoint-dir", type=Path, required=True)
    interactive_parser.add_argument("--pass-threshold", type=float)
    interactive_parser.add_argument("--block-threshold", type=float)

    predict_parser = subparsers.add_parser("predict", help="Classify text without logging the raw input")
    predict_parser.add_argument("--checkpoint-dir", type=Path, required=True)
    predict_parser.add_argument("--pass-threshold", type=float)
    predict_parser.add_argument("--block-threshold", type=float)
    source = predict_parser.add_mutually_exclusive_group()
    source.add_argument("--text")
    source.add_argument("--input-file", type=Path)
    return parser


def main(argv: list[str] | None = None) -> int:
    args = make_parser().parse_args(argv)
    if args.command == "train":
        logging.basicConfig(level=getattr(logging, args.log_level), format="%(levelname)s %(name)s %(message)s")
        config = TrainConfig(
            processed_dir=str(args.processed_dir.resolve()),
            output_dir=str(args.output_dir.resolve()),
            encoder_type=args.encoder_type,
            model_name=args.model_name,
            include_augmented=args.include_augmented,
            production_data_dir=str(args.production_data_dir.resolve()) if args.production_data_dir else None,
            production_negative_repeat=args.production_negative_repeat,
            max_length=args.max_length,
            batch_size=args.batch_size,
            epochs=args.epochs,
            learning_rate=args.learning_rate,
            encoder_learning_rate=args.encoder_learning_rate,
            weight_decay=args.weight_decay,
            auxiliary_weight=args.auxiliary_weight,
            consistency_weight=args.consistency_weight,
            representation_weight=args.representation_weight,
            gradient_accumulation=args.gradient_accumulation,
            seed=args.seed,
            amp=args.amp,
            limit_train=args.limit_train,
            limit_dev=args.limit_dev,
            target_block_precision=args.target_block_precision,
            target_pass_recall=args.target_pass_recall,
        )
        report = train(config)
        print(json.dumps({
            "output_dir": str(args.output_dir.resolve()),
            "best_epoch": report["best_epoch"],
            "calibrated_dev_metrics": report["calibrated_dev_metrics"],
            "threshold_policy": report["threshold_policy"],
            "policy_metrics": report["policy_metrics"],
        }, ensure_ascii=False, indent=2))
        return 0

    if args.command == "evaluate":
        report = evaluate_checkpoint(
            args.checkpoint_dir.resolve(),
            args.data_path.resolve(),
            args.output_path.resolve() if args.output_path is not None else None,
            batch_size=args.batch_size,
        )
        print(json.dumps(report, ensure_ascii=False, indent=2))
        return 0

    if args.command == "interactive":
        predictor = SafetyPredictor(
            args.checkpoint_dir.resolve(),
            pass_threshold=args.pass_threshold,
            block_threshold=args.block_threshold,
        )
        print("Model loaded. Enter text and press Enter; type /quit to exit.")
        while True:
            try:
                value = input("> ").strip()
            except (EOFError, KeyboardInterrupt):
                print()
                break
            if value.lower() in {"/quit", "/exit", "quit", "exit"}:
                break
            if not value:
                continue
            result = predictor.predict([value])[0]
            print(json.dumps(result, ensure_ascii=False, indent=2))
        return 0

    if args.command == "predict":
        if args.text is not None:
            texts = [args.text]
        elif args.input_file is not None:
            texts = [line.strip() for line in args.input_file.read_text(encoding="utf-8").splitlines() if line.strip()]
        else:
            value = sys.stdin.read().strip()
            texts = [value] if value else []
        if not texts:
            raise SystemExit("No text supplied")
        predictor = SafetyPredictor(
            args.checkpoint_dir.resolve(),
            pass_threshold=args.pass_threshold,
            block_threshold=args.block_threshold,
        )
        results = predictor.predict(texts)
        print(json.dumps(results, ensure_ascii=False, indent=2))
        return 0
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
