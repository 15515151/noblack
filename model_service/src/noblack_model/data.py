from __future__ import annotations

import json
from pathlib import Path
from typing import Any, Iterable, Sequence

import torch
from torch.utils.data import Dataset

from noblack_data.pipeline import pinyin_features
from .vocab import TokenVocabulary


def load_jsonl(path: Path, limit: int | None = None) -> list[dict[str, Any]]:
    records: list[dict[str, Any]] = []
    with path.open("r", encoding="utf-8") as handle:
        for line in handle:
            if not line.strip():
                continue
            records.append(json.loads(line))
            if limit is not None and len(records) >= limit:
                break
    return records


def load_training_records(
    processed_dir: Path,
    include_augmented: bool,
    limit: int | None = None,
) -> list[dict[str, Any]]:
    originals = load_jsonl(processed_dir / "sexharmset" / "train.jsonl", limit=None)
    records = originals
    if include_augmented:
        records = records + load_jsonl(processed_dir / "sexharmset" / "train_augmented.jsonl", limit=None)
    records = sorted(records, key=lambda row: row["id"])
    if limit is not None:
        # Deterministic balanced limiting rather than taking the first N IDs.
        harmful = [row for row in records if row["is_sexual_harmful"]]
        safe = [row for row in records if not row["is_sexual_harmful"]]
        per_label = max(1, limit // 2)
        records = harmful[:per_label] + safe[:per_label]
        records = sorted(records, key=lambda row: row["id"])[:limit]
    return records


class TextSafetyDataset(Dataset[dict[str, Any]]):
    def __init__(self, records: Sequence[dict[str, Any]]) -> None:
        self.records = list(records)

    def __len__(self) -> int:
        return len(self.records)

    def __getitem__(self, index: int) -> dict[str, Any]:
        row = self.records[index]
        pair_text = row.get("original_text") or row["text"]
        pair_pinyin = pinyin_features(pair_text)["pinyin_tone3"]
        return {
            "id": row["id"],
            "text": row["text"],
            "pinyin": row["pinyin_tone3"],
            "pair_text": pair_text,
            "pair_pinyin": pair_pinyin,
            "pair_mask": bool(row.get("is_augmented")),
            "label": int(bool(row["is_sexual_harmful"])),
        }


class BatchCollator:
    def __init__(
        self,
        pinyin_vocab: TokenVocabulary,
        max_length: int,
        encoder_type: str,
        char_vocab: TokenVocabulary | None = None,
        tokenizer: Any | None = None,
    ) -> None:
        self.pinyin_vocab = pinyin_vocab
        self.char_vocab = char_vocab
        self.tokenizer = tokenizer
        self.max_length = max_length
        self.encoder_type = encoder_type
        if encoder_type == "lite" and char_vocab is None:
            raise ValueError("char_vocab is required for lite encoder")
        if encoder_type == "pretrained" and tokenizer is None:
            raise ValueError("tokenizer is required for pretrained encoder")

    @staticmethod
    def _pad(sequences: Sequence[Sequence[int]], pad_id: int) -> tuple[torch.Tensor, torch.Tensor]:
        width = max(len(sequence) for sequence in sequences)
        ids = torch.full((len(sequences), width), pad_id, dtype=torch.long)
        mask = torch.zeros((len(sequences), width), dtype=torch.bool)
        for row_index, sequence in enumerate(sequences):
            length = len(sequence)
            ids[row_index, :length] = torch.tensor(sequence, dtype=torch.long)
            mask[row_index, :length] = True
        return ids, mask

    def _encode_texts(self, texts: Sequence[str]) -> tuple[torch.Tensor, torch.Tensor]:
        if self.encoder_type == "lite":
            assert self.char_vocab is not None
            values = [self.char_vocab.encode(list(text), self.max_length) for text in texts]
            return self._pad(values, self.char_vocab.pad_id)
        encoded = self.tokenizer(
            list(texts),
            truncation=True,
            padding=True,
            max_length=self.max_length,
            return_tensors="pt",
        )
        return encoded["input_ids"], encoded["attention_mask"].bool()

    def _encode_pinyin(self, sequences: Sequence[str]) -> tuple[torch.Tensor, torch.Tensor]:
        values = [self.pinyin_vocab.encode(sequence.split(), self.max_length) for sequence in sequences]
        return self._pad(values, self.pinyin_vocab.pad_id)

    def __call__(self, rows: Sequence[dict[str, Any]]) -> dict[str, Any]:
        text_ids, text_mask = self._encode_texts([row["text"] for row in rows])
        pinyin_ids, pinyin_mask = self._encode_pinyin([row["pinyin"] for row in rows])
        pair_text_ids, pair_text_mask = self._encode_texts([row["pair_text"] for row in rows])
        pair_pinyin_ids, pair_pinyin_mask = self._encode_pinyin([row["pair_pinyin"] for row in rows])
        return {
            "ids": [row["id"] for row in rows],
            "text_ids": text_ids,
            "text_mask": text_mask,
            "pinyin_ids": pinyin_ids,
            "pinyin_mask": pinyin_mask,
            "pair_text_ids": pair_text_ids,
            "pair_text_mask": pair_text_mask,
            "pair_pinyin_ids": pair_pinyin_ids,
            "pair_pinyin_mask": pair_pinyin_mask,
            "pair_mask": torch.tensor([row["pair_mask"] for row in rows], dtype=torch.bool),
            "labels": torch.tensor([row["label"] for row in rows], dtype=torch.long),
        }


def build_vocabs(
    training_records: Sequence[dict[str, Any]],
    max_char_vocab: int = 12000,
    max_pinyin_vocab: int = 4000,
) -> tuple[TokenVocabulary, TokenVocabulary]:
    char_sequences: Iterable[Iterable[str]] = (list(row["text"]) for row in training_records)
    pinyin_sequences: Iterable[Iterable[str]] = (row["pinyin_tone3"].split() for row in training_records)
    char_vocab = TokenVocabulary.build(char_sequences, max_size=max_char_vocab)
    pinyin_vocab = TokenVocabulary.build(pinyin_sequences, max_size=max_pinyin_vocab)
    return char_vocab, pinyin_vocab
