from __future__ import annotations

import json
from pathlib import Path
from typing import Any, Sequence

import torch

from noblack_data.pipeline import pinyin_features, stable_id
from .data import BatchCollator
from .training import load_checkpoint


HIGH_PRECISION_RULES = {
    "pornhub": "adult_site_name_pornhub",
}


def high_precision_rule_hits(text: str) -> list[str]:
    compact = "".join(character.lower() for character in text if character.isalnum())
    return [rule_id for pattern, rule_id in HIGH_PRECISION_RULES.items() if pattern in compact]


class SafetyPredictor:
    def __init__(
        self,
        checkpoint_dir: Path,
        pass_threshold: float | None = None,
        block_threshold: float | None = None,
    ) -> None:
        self.checkpoint_dir = checkpoint_dir
        self.device = torch.device("cuda" if torch.cuda.is_available() else "cpu")
        self.model, config_payload, self.pinyin_vocab, self.char_vocab, self.tokenizer = load_checkpoint(
            checkpoint_dir, self.device
        )
        model_config = config_payload["model"]
        training_config = config_payload["training"]
        self.collator = BatchCollator(
            pinyin_vocab=self.pinyin_vocab,
            char_vocab=self.char_vocab,
            tokenizer=self.tokenizer,
            max_length=int(training_config.get("max_length", 128)),
            encoder_type=model_config["encoder_type"],
        )
        threshold_path = checkpoint_dir / "thresholds.json"
        self.thresholds = (
            json.loads(threshold_path.read_text(encoding="utf-8"))
            if threshold_path.exists()
            else {"pass_threshold": 0.15, "block_threshold": 0.5}
        )
        if pass_threshold is not None:
            self.thresholds["pass_threshold"] = float(pass_threshold)
        if block_threshold is not None:
            self.thresholds["block_threshold"] = float(block_threshold)
        if self.thresholds["pass_threshold"] >= self.thresholds["block_threshold"]:
            raise ValueError("pass_threshold must be lower than block_threshold")

    def predict(self, texts: Sequence[str], batch_size: int = 32) -> list[dict[str, Any]]:
        results: list[dict[str, Any]] = []
        self.model.eval()
        with torch.no_grad():
            for offset in range(0, len(texts), batch_size):
                chunk = list(texts[offset : offset + batch_size])
                rows = []
                for text in chunk:
                    pinyin = pinyin_features(text)["pinyin_tone3"]
                    rows.append(
                        {
                            "id": stable_id("inference", text),
                            "text": text,
                            "pinyin": pinyin,
                            "pair_text": text,
                            "pair_pinyin": pinyin,
                            "pair_mask": False,
                            "label": 0,
                        }
                    )
                batch = self.collator(rows)
                tensor_batch = {
                    key: value.to(self.device) if isinstance(value, torch.Tensor) else value
                    for key, value in batch.items()
                }
                outputs = self.model(
                    tensor_batch["text_ids"],
                    tensor_batch["text_mask"],
                    tensor_batch["pinyin_ids"],
                    tensor_batch["pinyin_mask"],
                )
                probabilities = torch.softmax(outputs["logits"], dim=-1)[:, 1].float().cpu().tolist()
                semantic_gates = outputs["gate"].float().mean(dim=-1).cpu().tolist()
                for row, probability, semantic_gate in zip(rows, probabilities, semantic_gates):
                    rule_hits = high_precision_rule_hits(row["text"])
                    if rule_hits:
                        probability = max(probability, 0.99)
                    if probability < self.thresholds["pass_threshold"]:
                        action = "pass"
                    elif probability >= self.thresholds["block_threshold"]:
                        action = "block"
                    else:
                        action = "review"
                    results.append(
                        {
                            "id": row["id"],
                            "sexual_harm_probability": probability,
                            "action": action,
                            "semantic_gate": semantic_gate,
                            "rule_hits": rule_hits,
                            "pass_threshold": self.thresholds["pass_threshold"],
                            "block_threshold": self.thresholds["block_threshold"],
                        }
                    )
        return results


def predict_texts(checkpoint_dir: Path, texts: Sequence[str], batch_size: int = 32) -> list[dict[str, Any]]:
    return SafetyPredictor(checkpoint_dir).predict(texts, batch_size=batch_size)
