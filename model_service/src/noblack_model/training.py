from __future__ import annotations

import json
import logging
import math
import random
import time
from dataclasses import asdict, dataclass
from pathlib import Path
from typing import Any, Sequence

import numpy as np
import torch
from safetensors.torch import load_file, save_file
from torch.utils.data import DataLoader

from .data import BatchCollator, TextSafetyDataset, build_vocabs, load_jsonl, load_training_records
from .metrics import average_precision, binary_metrics, choose_threshold_policy, policy_metrics
from .modeling import DualBranchClassifier, ModelConfig, compute_training_loss
from .vocab import TokenVocabulary

LOGGER = logging.getLogger("noblack_model")


@dataclass(frozen=True)
class TrainConfig:
    processed_dir: str
    output_dir: str
    encoder_type: str = "lite"
    model_name: str = "hfl/chinese-macbert-base"
    include_augmented: bool = True
    production_data_dir: str | None = None
    production_negative_repeat: int = 1
    max_length: int = 128
    batch_size: int = 32
    epochs: int = 3
    learning_rate: float = 2e-4
    encoder_learning_rate: float = 2e-5
    weight_decay: float = 0.01
    auxiliary_weight: float = 0.1
    consistency_weight: float = 0.2
    representation_weight: float = 0.1
    gradient_clip: float = 1.0
    gradient_accumulation: int = 1
    seed: int = 20260714
    amp: bool = True
    limit_train: int | None = None
    limit_dev: int | None = None
    target_block_precision: float = 0.9
    target_pass_recall: float = 0.98


def set_seed(seed: int) -> None:
    random.seed(seed)
    np.random.seed(seed)
    torch.manual_seed(seed)
    if torch.cuda.is_available():
        torch.cuda.manual_seed_all(seed)


def _to_device(batch: dict[str, Any], device: torch.device) -> dict[str, Any]:
    return {key: value.to(device) if isinstance(value, torch.Tensor) else value for key, value in batch.items()}


def _parameter_groups(model: DualBranchClassifier, config: TrainConfig) -> list[dict[str, Any]]:
    if config.encoder_type != "pretrained":
        return [{"params": model.parameters(), "lr": config.learning_rate}]
    encoder_ids = {id(parameter) for parameter in model.semantic_encoder.parameters()}
    encoder_parameters = [parameter for parameter in model.parameters() if id(parameter) in encoder_ids]
    task_parameters = [parameter for parameter in model.parameters() if id(parameter) not in encoder_ids]
    return [
        {"params": encoder_parameters, "lr": config.encoder_learning_rate},
        {"params": task_parameters, "lr": config.learning_rate},
    ]


def evaluate(
    model: DualBranchClassifier,
    loader: DataLoader,
    device: torch.device,
    amp: bool,
) -> tuple[list[int], list[float], dict[str, float]]:
    model.eval()
    labels: list[int] = []
    probabilities: list[float] = []
    gate_sum = 0.0
    gate_count = 0
    with torch.no_grad():
        for batch in loader:
            batch = _to_device(batch, device)
            with torch.autocast(device_type=device.type, dtype=torch.float16, enabled=amp and device.type == "cuda"):
                outputs = model(
                    batch["text_ids"], batch["text_mask"], batch["pinyin_ids"], batch["pinyin_mask"]
                )
            probability = torch.softmax(outputs["logits"], dim=-1)[:, 1]
            labels.extend(batch["labels"].cpu().tolist())
            probabilities.extend(probability.float().cpu().tolist())
            gate_sum += float(outputs["gate"].float().sum().cpu())
            gate_count += outputs["gate"].numel()
    return labels, probabilities, {"mean_semantic_gate": gate_sum / max(1, gate_count)}


def save_checkpoint(
    output_dir: Path,
    model: DualBranchClassifier,
    model_config: ModelConfig,
    train_config: TrainConfig,
    char_vocab: TokenVocabulary | None,
    pinyin_vocab: TokenVocabulary,
    tokenizer: Any | None,
    metrics: dict[str, Any],
) -> None:
    output_dir.mkdir(parents=True, exist_ok=True)
    state = {key: value.detach().cpu().contiguous() for key, value in model.state_dict().items()}
    save_file(state, output_dir / "model.safetensors")
    (output_dir / "model_config.json").write_text(
        json.dumps({"model": asdict(model_config), "training": asdict(train_config)}, ensure_ascii=False, indent=2) + "\n",
        encoding="utf-8",
    )
    pinyin_vocab.save(output_dir / "pinyin_vocab.json")
    if char_vocab is not None:
        char_vocab.save(output_dir / "char_vocab.json")
    if tokenizer is not None:
        tokenizer.save_pretrained(output_dir / "tokenizer")
    if model_config.encoder_type == "pretrained":
        model.semantic_encoder.model.config.save_pretrained(output_dir / "backbone_config")
    (output_dir / "metrics.json").write_text(json.dumps(metrics, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


def load_checkpoint(output_dir: Path, device: torch.device) -> tuple[DualBranchClassifier, dict[str, Any], TokenVocabulary, TokenVocabulary | None, Any | None]:
    config_payload = json.loads((output_dir / "model_config.json").read_text(encoding="utf-8"))
    model_config = ModelConfig(**config_payload["model"])
    pinyin_vocab = TokenVocabulary.load(output_dir / "pinyin_vocab.json")
    char_vocab = TokenVocabulary.load(output_dir / "char_vocab.json") if (output_dir / "char_vocab.json").exists() else None
    tokenizer = None
    if model_config.encoder_type == "pretrained":
        from transformers import AutoTokenizer

        tokenizer_path = output_dir / "tokenizer"
        tokenizer = AutoTokenizer.from_pretrained(tokenizer_path if tokenizer_path.exists() else model_config.model_name)
    backbone_config_path = output_dir / "backbone_config"
    model = DualBranchClassifier(
        model_config,
        pretrained_config_path=str(backbone_config_path) if backbone_config_path.exists() else None,
    )
    model.load_state_dict(load_file(output_dir / "model.safetensors", device=str(device)))
    model.to(device)
    model.eval()
    return model, config_payload, pinyin_vocab, char_vocab, tokenizer


def train(config: TrainConfig) -> dict[str, Any]:
    set_seed(config.seed)
    processed_dir = Path(config.processed_dir).resolve()
    output_dir = Path(config.output_dir).resolve()
    device = torch.device("cuda" if torch.cuda.is_available() else "cpu")
    use_amp = bool(config.amp and device.type == "cuda")

    train_records = load_training_records(processed_dir, config.include_augmented, config.limit_train)
    dev_records = load_jsonl(processed_dir / "sexharmset" / "dev.jsonl", config.limit_dev)
    production_train_records: list[dict[str, Any]] = []
    production_dev_records: list[dict[str, Any]] = []
    if config.production_data_dir:
        if config.production_negative_repeat < 1:
            raise ValueError("production_negative_repeat must be at least 1")
        production_dir = Path(config.production_data_dir).resolve()
        production_train_records = load_jsonl(production_dir / "train.jsonl")
        production_dev_records = load_jsonl(production_dir / "dev.jsonl")
        train_records = train_records + production_train_records * config.production_negative_repeat
        dev_records = dev_records + production_dev_records
    if not train_records or not dev_records:
        raise RuntimeError("Training and development data must not be empty")

    char_vocab, pinyin_vocab = build_vocabs(train_records)
    tokenizer = None
    if config.encoder_type == "pretrained":
        from transformers import AutoTokenizer

        tokenizer = AutoTokenizer.from_pretrained(config.model_name)
        char_vocab_for_model = None
        char_vocab_size = 0
    else:
        char_vocab_for_model = char_vocab
        char_vocab_size = len(char_vocab)

    collator = BatchCollator(
        pinyin_vocab=pinyin_vocab,
        char_vocab=char_vocab_for_model,
        tokenizer=tokenizer,
        max_length=config.max_length,
        encoder_type=config.encoder_type,
    )
    generator = torch.Generator().manual_seed(config.seed)
    train_loader = DataLoader(
        TextSafetyDataset(train_records),
        batch_size=config.batch_size,
        shuffle=True,
        generator=generator,
        collate_fn=collator,
        num_workers=0,
        pin_memory=device.type == "cuda",
    )
    dev_loader = DataLoader(
        TextSafetyDataset(dev_records),
        batch_size=max(config.batch_size, 32),
        shuffle=False,
        collate_fn=collator,
        num_workers=0,
        pin_memory=device.type == "cuda",
    )

    model_config = ModelConfig(
        encoder_type=config.encoder_type,
        model_name=config.model_name,
        char_vocab_size=char_vocab_size,
        pinyin_vocab_size=len(pinyin_vocab),
    )
    model = DualBranchClassifier(model_config).to(device)
    optimizer = torch.optim.AdamW(
        _parameter_groups(model, config),
        weight_decay=config.weight_decay,
    )
    scaler = torch.amp.GradScaler("cuda", enabled=use_amp)
    total_optimizer_steps = math.ceil(len(train_loader) / max(1, config.gradient_accumulation)) * config.epochs
    scheduler = torch.optim.lr_scheduler.CosineAnnealingLR(optimizer, T_max=max(1, total_optimizer_steps))

    LOGGER.info(
        "training start encoder=%s device=%s train_rows=%d dev_rows=%d batch_size=%d",
        config.encoder_type,
        device,
        len(train_records),
        len(dev_records),
        config.batch_size,
    )
    best_f1 = -1.0
    best_epoch = 0
    history: list[dict[str, Any]] = []
    started = time.time()
    optimizer.zero_grad(set_to_none=True)

    for epoch in range(1, config.epochs + 1):
        model.train()
        loss_sums: dict[str, float] = {key: 0.0 for key in ("classification", "auxiliary", "consistency", "representation", "total")}
        batch_count = 0
        for batch_index, batch in enumerate(train_loader, start=1):
            batch = _to_device(batch, device)
            with torch.autocast(device_type=device.type, dtype=torch.float16, enabled=use_amp):
                outputs = model(
                    batch["text_ids"], batch["text_mask"], batch["pinyin_ids"], batch["pinyin_mask"]
                )
                if batch["pair_mask"].any():
                    pair_outputs = model(
                        batch["pair_text_ids"],
                        batch["pair_text_mask"],
                        batch["pair_pinyin_ids"],
                        batch["pair_pinyin_mask"],
                    )
                else:
                    pair_outputs = outputs
                loss, parts = compute_training_loss(
                    outputs,
                    pair_outputs,
                    batch["labels"],
                    batch["pair_mask"],
                    config.auxiliary_weight,
                    config.consistency_weight,
                    config.representation_weight,
                )
                scaled_loss = loss / max(1, config.gradient_accumulation)
            scaler.scale(scaled_loss).backward()
            if batch_index % config.gradient_accumulation == 0 or batch_index == len(train_loader):
                scaler.unscale_(optimizer)
                torch.nn.utils.clip_grad_norm_(model.parameters(), config.gradient_clip)
                scaler.step(optimizer)
                scaler.update()
                optimizer.zero_grad(set_to_none=True)
                scheduler.step()
            for key, value in parts.items():
                loss_sums[key] += value
            batch_count += 1

        labels, probabilities, diagnostics = evaluate(model, dev_loader, device, use_amp)
        metrics = binary_metrics(labels, probabilities, threshold=0.5)
        metrics["average_precision"] = average_precision(labels, probabilities)
        metrics.update(diagnostics)
        epoch_record = {
            "epoch": epoch,
            "losses": {key: value / max(1, batch_count) for key, value in loss_sums.items()},
            "dev": metrics,
            "learning_rates": [group["lr"] for group in optimizer.param_groups],
        }
        history.append(epoch_record)
        LOGGER.info(
            "epoch=%d loss=%.5f dev_f1=%.5f dev_precision=%.5f dev_recall=%.5f ap=%.5f",
            epoch,
            epoch_record["losses"]["total"],
            metrics["f1"],
            metrics["precision"],
            metrics["recall"],
            metrics["average_precision"],
        )
        if float(metrics["f1"]) > best_f1:
            best_f1 = float(metrics["f1"])
            best_epoch = epoch
            save_checkpoint(
                output_dir,
                model,
                model_config,
                config,
                char_vocab_for_model,
                pinyin_vocab,
                tokenizer,
                {"best_epoch": best_epoch, "history": history},
            )

    # Reload the selected checkpoint before threshold calibration.
    model.load_state_dict(load_file(output_dir / "model.safetensors", device=str(device)))
    labels, probabilities, diagnostics = evaluate(model, dev_loader, device, use_amp)
    policy = choose_threshold_policy(
        labels,
        probabilities,
        target_block_precision=config.target_block_precision,
        target_pass_recall=config.target_pass_recall,
    )
    calibrated_metrics = binary_metrics(labels, probabilities, threshold=policy.block_threshold)
    calibrated_metrics["average_precision"] = average_precision(labels, probabilities)
    calibrated_metrics.update(diagnostics)
    policy_report = policy_metrics(labels, probabilities, policy)
    final_report = {
        "best_epoch": best_epoch,
        "best_f1_at_0_5": best_f1,
        "history": history,
        "calibrated_dev_metrics": calibrated_metrics,
        "threshold_policy": asdict(policy),
        "policy_metrics": policy_report,
        "runtime_seconds": time.time() - started,
        "environment": {
            "device": str(device),
            "torch_version": torch.__version__,
            "cuda_version": torch.version.cuda,
            "cuda_device": torch.cuda.get_device_name(0) if device.type == "cuda" else None,
        },
        "data": {
            "train_rows": len(train_records),
            "dev_rows": len(dev_records),
            "production_train_unique_rows": len(production_train_records),
            "production_train_effective_rows": len(production_train_records) * config.production_negative_repeat,
            "production_dev_rows": len(production_dev_records),
            "raw_text_in_logs": False,
        },
    }
    save_checkpoint(
        output_dir,
        model,
        model_config,
        config,
        char_vocab_for_model,
        pinyin_vocab,
        tokenizer,
        final_report,
    )
    (output_dir / "thresholds.json").write_text(
        json.dumps(asdict(policy), ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
    )
    LOGGER.info(
        "training complete best_epoch=%d block_threshold=%.4f pass_threshold=%.4f runtime_seconds=%.1f",
        best_epoch,
        policy.block_threshold,
        policy.pass_threshold,
        final_report["runtime_seconds"],
    )
    return final_report


def evaluate_checkpoint(
    checkpoint_dir: Path,
    data_path: Path,
    output_path: Path | None = None,
    batch_size: int = 64,
) -> dict[str, Any]:
    device = torch.device("cuda" if torch.cuda.is_available() else "cpu")
    model, config_payload, pinyin_vocab, char_vocab, tokenizer = load_checkpoint(checkpoint_dir, device)
    records = load_jsonl(data_path)
    training_config = config_payload["training"]
    collator = BatchCollator(
        pinyin_vocab=pinyin_vocab,
        char_vocab=char_vocab,
        tokenizer=tokenizer,
        max_length=int(training_config.get("max_length", 128)),
        encoder_type=config_payload["model"]["encoder_type"],
    )
    loader = DataLoader(
        TextSafetyDataset(records),
        batch_size=batch_size,
        shuffle=False,
        collate_fn=collator,
        num_workers=0,
        pin_memory=device.type == "cuda",
    )
    labels, probabilities, diagnostics = evaluate(model, loader, device, bool(training_config.get("amp", True)))
    threshold_path = checkpoint_dir / "thresholds.json"
    policy_payload = json.loads(threshold_path.read_text(encoding="utf-8"))
    from .metrics import ThresholdPolicy

    policy = ThresholdPolicy(**policy_payload)
    metrics_by_evasion_type: dict[str, Any] = {}
    evasion_types = sorted({
        evasion
        for row in records
        for evasion in (row.get("evasion_types") or ["none"])
    })
    for evasion in evasion_types:
        indices = [
            index for index, row in enumerate(records)
            if evasion in (row.get("evasion_types") or ["none"])
        ]
        group_labels = [labels[index] for index in indices]
        group_probabilities = [probabilities[index] for index in indices]
        metrics_by_evasion_type[evasion] = {
            "rows": len(indices),
            "metrics_at_0_5": binary_metrics(group_labels, group_probabilities, threshold=0.5),
            "metrics_at_block_threshold": binary_metrics(
                group_labels, group_probabilities, threshold=policy.block_threshold
            ),
            "average_precision": average_precision(group_labels, group_probabilities),
        }

    report = {
        "checkpoint_dir": str(checkpoint_dir.resolve()),
        "data_path": str(data_path.resolve()),
        "rows": len(records),
        "metrics_at_0_5": binary_metrics(labels, probabilities, threshold=0.5),
        "metrics_at_block_threshold": binary_metrics(labels, probabilities, threshold=policy.block_threshold),
        "average_precision": average_precision(labels, probabilities),
        "diagnostics": diagnostics,
        "threshold_policy": policy_payload,
        "policy_metrics": policy_metrics(labels, probabilities, policy),
        "metrics_by_evasion_type": metrics_by_evasion_type,
        "raw_text_in_report": False,
    }
    if output_path is not None:
        output_path.parent.mkdir(parents=True, exist_ok=True)
        output_path.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    return report
