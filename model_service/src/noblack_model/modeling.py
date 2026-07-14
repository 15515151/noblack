from __future__ import annotations

from dataclasses import dataclass
from typing import Any

import torch
from torch import nn


@dataclass(frozen=True)
class ModelConfig:
    encoder_type: str = "lite"
    model_name: str = "hfl/chinese-macbert-base"
    char_vocab_size: int = 0
    pinyin_vocab_size: int = 0
    char_embedding_dim: int = 192
    semantic_hidden_dim: int = 256
    pinyin_embedding_dim: int = 96
    pinyin_hidden_dim: int = 192
    fusion_dim: int = 256
    dropout: float = 0.2
    num_labels: int = 2


class MaskedBiGRUEncoder(nn.Module):
    def __init__(self, vocab_size: int, embedding_dim: int, hidden_dim: int, pad_id: int = 0) -> None:
        super().__init__()
        if hidden_dim % 2:
            raise ValueError("hidden_dim must be even for bidirectional GRU")
        self.embedding = nn.Embedding(vocab_size, embedding_dim, padding_idx=pad_id)
        self.gru = nn.GRU(
            embedding_dim,
            hidden_dim // 2,
            batch_first=True,
            bidirectional=True,
        )
        self.output_dim = hidden_dim * 2

    def forward(self, input_ids: torch.Tensor, attention_mask: torch.Tensor) -> torch.Tensor:
        embedded = self.embedding(input_ids)
        encoded, _ = self.gru(embedded)
        mask = attention_mask.unsqueeze(-1)
        masked = encoded * mask
        denominator = mask.sum(dim=1).clamp_min(1)
        mean_pool = masked.sum(dim=1) / denominator
        max_pool = encoded.masked_fill(~mask, torch.finfo(encoded.dtype).min).max(dim=1).values
        return torch.cat([mean_pool, max_pool], dim=-1)


class PretrainedSemanticEncoder(nn.Module):
    def __init__(self, model_name: str, config_path: str | None = None) -> None:
        super().__init__()
        from transformers import AutoConfig, AutoModel

        if config_path is not None:
            backbone_config = AutoConfig.from_pretrained(config_path, local_files_only=True)
            self.model = AutoModel.from_config(backbone_config)
        else:
            self.model = AutoModel.from_pretrained(model_name)
        self.output_dim = int(self.model.config.hidden_size)

    def forward(self, input_ids: torch.Tensor, attention_mask: torch.Tensor) -> torch.Tensor:
        output = self.model(input_ids=input_ids, attention_mask=attention_mask.long())
        hidden = output.last_hidden_state
        # Masked mean is generally more stable than relying on a task-untrained pooler.
        mask = attention_mask.unsqueeze(-1)
        return (hidden * mask).sum(dim=1) / mask.sum(dim=1).clamp_min(1)


class DualBranchClassifier(nn.Module):
    def __init__(self, config: ModelConfig, pretrained_config_path: str | None = None) -> None:
        super().__init__()
        self.config = config
        if config.encoder_type == "lite":
            self.semantic_encoder: nn.Module = MaskedBiGRUEncoder(
                config.char_vocab_size,
                config.char_embedding_dim,
                config.semantic_hidden_dim,
            )
        elif config.encoder_type == "pretrained":
            self.semantic_encoder = PretrainedSemanticEncoder(config.model_name, pretrained_config_path)
        else:
            raise ValueError(f"Unsupported encoder_type: {config.encoder_type}")

        self.pinyin_encoder = MaskedBiGRUEncoder(
            config.pinyin_vocab_size,
            config.pinyin_embedding_dim,
            config.pinyin_hidden_dim,
        )
        self.semantic_projection = nn.Sequential(
            nn.Linear(self.semantic_encoder.output_dim, config.fusion_dim),
            nn.LayerNorm(config.fusion_dim),
            nn.GELU(),
        )
        self.pinyin_projection = nn.Sequential(
            nn.Linear(self.pinyin_encoder.output_dim, config.fusion_dim),
            nn.LayerNorm(config.fusion_dim),
            nn.GELU(),
        )
        self.gate = nn.Sequential(
            nn.Linear(config.fusion_dim * 2, config.fusion_dim),
            nn.Sigmoid(),
        )
        self.dropout = nn.Dropout(config.dropout)
        self.classifier = nn.Linear(config.fusion_dim, config.num_labels)
        self.semantic_head = nn.Linear(config.fusion_dim, config.num_labels)
        self.pinyin_head = nn.Linear(config.fusion_dim, config.num_labels)

    def forward(
        self,
        text_ids: torch.Tensor,
        text_mask: torch.Tensor,
        pinyin_ids: torch.Tensor,
        pinyin_mask: torch.Tensor,
    ) -> dict[str, torch.Tensor]:
        semantic_raw = self.semantic_encoder(text_ids, text_mask)
        pinyin_raw = self.pinyin_encoder(pinyin_ids, pinyin_mask)
        semantic = self.semantic_projection(semantic_raw)
        pinyin = self.pinyin_projection(pinyin_raw)
        gate = self.gate(torch.cat([semantic, pinyin], dim=-1))
        fused = gate * semantic + (1.0 - gate) * pinyin
        fused = self.dropout(fused)
        return {
            "logits": self.classifier(fused),
            "semantic_logits": self.semantic_head(self.dropout(semantic)),
            "pinyin_logits": self.pinyin_head(self.dropout(pinyin)),
            "fused": fused,
            "semantic": semantic,
            "pinyin": pinyin,
            "gate": gate,
        }


def symmetric_kl(left_logits: torch.Tensor, right_logits: torch.Tensor) -> torch.Tensor:
    left_log = torch.log_softmax(left_logits, dim=-1)
    right_log = torch.log_softmax(right_logits, dim=-1)
    left_prob = left_log.exp()
    right_prob = right_log.exp()
    left_to_right = torch.nn.functional.kl_div(left_log, right_prob, reduction="none").sum(dim=-1)
    right_to_left = torch.nn.functional.kl_div(right_log, left_prob, reduction="none").sum(dim=-1)
    return 0.5 * (left_to_right + right_to_left)


def compute_training_loss(
    outputs: dict[str, torch.Tensor],
    pair_outputs: dict[str, torch.Tensor],
    labels: torch.Tensor,
    pair_mask: torch.Tensor,
    auxiliary_weight: float,
    consistency_weight: float,
    representation_weight: float,
) -> tuple[torch.Tensor, dict[str, float]]:
    classification = torch.nn.functional.cross_entropy(outputs["logits"], labels)
    auxiliary = 0.5 * (
        torch.nn.functional.cross_entropy(outputs["semantic_logits"], labels)
        + torch.nn.functional.cross_entropy(outputs["pinyin_logits"], labels)
    )
    if pair_mask.any():
        active = pair_mask.nonzero(as_tuple=False).squeeze(-1)
        consistency = symmetric_kl(outputs["logits"][active], pair_outputs["logits"][active]).mean()
        representation = (1.0 - torch.nn.functional.cosine_similarity(
            outputs["fused"][active], pair_outputs["fused"][active], dim=-1
        )).mean()
    else:
        consistency = classification.new_zeros(())
        representation = classification.new_zeros(())
    total = (
        classification
        + auxiliary_weight * auxiliary
        + consistency_weight * consistency
        + representation_weight * representation
    )
    return total, {
        "classification": float(classification.detach()),
        "auxiliary": float(auxiliary.detach()),
        "consistency": float(consistency.detach()),
        "representation": float(representation.detach()),
        "total": float(total.detach()),
    }
