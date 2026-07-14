from __future__ import annotations

import json
from collections import Counter
from dataclasses import dataclass
from pathlib import Path
from typing import Iterable, Sequence


PAD_TOKEN = "<pad>"
UNK_TOKEN = "<unk>"


@dataclass
class TokenVocabulary:
    token_to_id: dict[str, int]

    @classmethod
    def build(cls, sequences: Iterable[Iterable[str]], max_size: int | None = None, min_frequency: int = 1) -> "TokenVocabulary":
        counts: Counter[str] = Counter()
        for sequence in sequences:
            counts.update(sequence)
        ordered = sorted(
            ((token, count) for token, count in counts.items() if count >= min_frequency),
            key=lambda item: (-item[1], item[0]),
        )
        if max_size is not None:
            ordered = ordered[: max(0, max_size - 2)]
        mapping = {PAD_TOKEN: 0, UNK_TOKEN: 1}
        for token, _ in ordered:
            if token not in mapping:
                mapping[token] = len(mapping)
        return cls(mapping)

    @property
    def pad_id(self) -> int:
        return self.token_to_id[PAD_TOKEN]

    @property
    def unk_id(self) -> int:
        return self.token_to_id[UNK_TOKEN]

    def __len__(self) -> int:
        return len(self.token_to_id)

    def encode(self, tokens: Sequence[str], max_length: int) -> list[int]:
        values = [self.token_to_id.get(token, self.unk_id) for token in tokens[:max_length]]
        return values or [self.unk_id]

    def save(self, path: Path) -> None:
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(self.token_to_id, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    @classmethod
    def load(cls, path: Path) -> "TokenVocabulary":
        return cls(json.loads(path.read_text(encoding="utf-8")))
