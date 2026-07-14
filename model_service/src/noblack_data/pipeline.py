from __future__ import annotations

import csv
import hashlib
import json
import logging
import math
import re
import unicodedata
from collections import Counter, defaultdict
from dataclasses import asdict, dataclass, replace
from difflib import SequenceMatcher
from pathlib import Path
from typing import Any, Iterable, Iterator, Sequence

try:
    from pypinyin import Style, lazy_pinyin
except ImportError as exc:  # pragma: no cover - covered by CLI dependency check
    raise RuntimeError(
        "pypinyin is required. Run: python -m pip install --target .vendor pypinyin==0.55.0"
    ) from exc


LOGGER = logging.getLogger("noblack_data")
WS_RE = re.compile(r"\s+")
NON_WORD_RE = re.compile(r"[^\w\u3400-\u9fff]+", re.UNICODE)
SOURCE_SEXHARM = "zhatebench_sexharmset"
SOURCE_HED = "hed_cold"


@dataclass(frozen=True)
class BuildConfig:
    seed: int = 20260714
    train_ratio: float = 0.8
    dev_ratio: float = 0.1
    max_per_template: int = 3
    near_duplicate_hamming: int = 3
    near_duplicate_jaccard: float = 0.88
    augmentations_per_row: int = 3


class SafeLogger:
    """Logger that only exposes IDs, hashes and aggregate counts—not raw samples."""

    def __init__(self, logger: logging.Logger | None = None) -> None:
        self.logger = logger or LOGGER

    @staticmethod
    def text_ref(text: str) -> str:
        return hashlib.sha256(text.encode("utf-8", errors="replace")).hexdigest()[:12]

    def info(self, message: str, **fields: Any) -> None:
        clean: dict[str, Any] = {}
        for key, value in fields.items():
            lowered = key.lower()
            if any(token in lowered for token in ("text", "sentence", "keyword", "content")):
                if value is not None:
                    clean[f"{key}_sha256_12"] = self.text_ref(str(value))
            else:
                clean[key] = value
        suffix = " ".join(f"{k}={v}" for k, v in sorted(clean.items()))
        self.logger.info("%s%s", message, f" {suffix}" if suffix else "")


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def detect_encoding(path: Path) -> str:
    # These datasets are small enough to validate the complete byte stream.
    # Decoding a fixed-size prefix can falsely reject UTF-8 when the prefix ends
    # in the middle of a multibyte character.
    raw = path.read_bytes()
    if raw.startswith(b"\xef\xbb\xbf"):
        return "utf-8-sig"
    if raw.startswith((b"\xff\xfe", b"\xfe\xff")):
        return "utf-16"
    for encoding in ("utf-8", "gb18030"):
        try:
            raw.decode(encoding)
            return encoding
        except UnicodeDecodeError:
            continue
    raise UnicodeError(f"Cannot detect supported encoding for {path}")


def read_csv_rows(path: Path) -> tuple[list[dict[str, str]], str]:
    encoding = detect_encoding(path)
    with path.open("r", encoding=encoding, newline="") as handle:
        reader = csv.DictReader(handle)
        rows = [dict(row) for row in reader]
    return rows, encoding


def write_jsonl(path: Path, rows: Iterable[dict[str, Any]]) -> int:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(path.suffix + ".tmp")
    count = 0
    with temporary.open("w", encoding="utf-8", newline="\n") as handle:
        for row in rows:
            handle.write(json.dumps(row, ensure_ascii=False, separators=(",", ":")) + "\n")
            count += 1
    temporary.replace(path)
    return count


def write_json(path: Path, value: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    temporary.replace(path)


def normalize_text(text: str) -> str:
    text = unicodedata.normalize("NFKC", text or "")
    text = WS_RE.sub(" ", text).strip().lower()
    return text


def compact_text(text: str) -> str:
    return NON_WORD_RE.sub("", normalize_text(text))


def stable_id(*parts: str, length: int = 16) -> str:
    payload = "\0".join(parts).encode("utf-8", errors="replace")
    return hashlib.sha256(payload).hexdigest()[:length]


def deterministic_rank(seed: int, value: str) -> str:
    return hashlib.sha256(f"{seed}\0{value}".encode("utf-8")).hexdigest()


def pinyin_features(text: str) -> dict[str, str]:
    tone3 = lazy_pinyin(
        text,
        style=Style.TONE3,
        neutral_tone_with_five=True,
        errors=lambda chars: list(chars),
    )
    normal = lazy_pinyin(text, style=Style.NORMAL, errors=lambda chars: list(chars))
    initials = lazy_pinyin(text, style=Style.FIRST_LETTER, errors=lambda chars: list(chars))
    return {
        "pinyin_tone3": " ".join(tone3),
        "pinyin_normal": " ".join(normal),
        "pinyin_initials": " ".join(initials),
    }


def _ngrams(text: str, size: int = 3) -> set[str]:
    compact = compact_text(text)
    if not compact:
        return {"<empty>"}
    if len(compact) <= size:
        return {compact}
    return {compact[index : index + size] for index in range(len(compact) - size + 1)}


def simhash64(features: Iterable[str]) -> int:
    vector = [0] * 64
    values = list(features) or ["<empty>"]
    for feature in values:
        hashed = int.from_bytes(hashlib.blake2b(feature.encode("utf-8"), digest_size=8).digest(), "big")
        for bit in range(64):
            vector[bit] += 1 if hashed & (1 << bit) else -1
    result = 0
    for bit, score in enumerate(vector):
        if score >= 0:
            result |= 1 << bit
    return result


def hamming_distance(left: int, right: int) -> int:
    return (left ^ right).bit_count()


def jaccard(left: set[str], right: set[str]) -> float:
    union = left | right
    return 1.0 if not union else len(left & right) / len(union)


class NearDuplicateIndex:
    def __init__(self, max_hamming: int, min_jaccard: float) -> None:
        self.max_hamming = max_hamming
        self.min_jaccard = min_jaccard
        self.entries: list[tuple[int, set[str], str]] = []
        self.buckets: dict[tuple[int, int], list[int]] = defaultdict(list)

    @staticmethod
    def _bands(value: int) -> Iterator[tuple[int, int]]:
        for band in range(4):
            yield band, (value >> (band * 16)) & 0xFFFF

    def add(self, text: str, record_id: str) -> None:
        features = _ngrams(text)
        signature = simhash64(features)
        index = len(self.entries)
        self.entries.append((signature, features, record_id))
        for key in self._bands(signature):
            self.buckets[key].append(index)

    def find(self, text: str) -> str | None:
        features = _ngrams(text)
        signature = simhash64(features)
        candidates: set[int] = set()
        for key in self._bands(signature):
            candidates.update(self.buckets.get(key, ()))
        for index in sorted(candidates):
            known_hash, known_features, record_id = self.entries[index]
            if hamming_distance(signature, known_hash) <= self.max_hamming:
                if jaccard(features, known_features) >= self.min_jaccard:
                    return record_id
        return None


def mask_keyword(text: str, keyword: str) -> str:
    normalized_text = normalize_text(text)
    normalized_keyword = normalize_text(keyword)
    if normalized_keyword and normalized_keyword in normalized_text:
        return normalized_text.replace(normalized_keyword, "<kw>")
    return normalized_text


def assign_keyword_splits(keywords: Sequence[str], config: BuildConfig) -> dict[str, str]:
    unique = sorted(set(keywords), key=lambda item: deterministic_rank(config.seed, normalize_text(item)))
    total = len(unique)
    train_count = int(round(total * config.train_ratio))
    dev_count = int(round(total * config.dev_ratio))
    train_count = min(train_count, total)
    dev_count = min(dev_count, total - train_count)
    result: dict[str, str] = {}
    for index, keyword in enumerate(unique):
        if index < train_count:
            split = "train"
        elif index < train_count + dev_count:
            split = "dev"
        else:
            split = "test"
        result[keyword] = split
    return result


def prepare_sexharm_rows(
    input_path: Path,
    config: BuildConfig,
    safe_logger: SafeLogger,
) -> tuple[dict[str, list[dict[str, Any]]], dict[str, Any]]:
    raw_rows, encoding = read_csv_rows(input_path)
    parsed: list[dict[str, Any]] = []
    invalid = 0
    label_by_text: dict[str, set[str]] = defaultdict(set)
    for row_index, row in enumerate(raw_rows, start=1):
        keyword = (row.get("Keyword") or "").strip()
        label = (row.get("Type") or "").strip()
        sentence = (row.get("Sentence") or "").strip()
        if not keyword or label not in {"Harmful", "Safe"} or not sentence:
            invalid += 1
            continue
        normalized = normalize_text(sentence)
        label_by_text[normalized].add(label)
        parsed.append(
            {
                "source_row": row_index,
                "keyword": keyword,
                "label": label,
                "text": sentence,
                "normalized_text": normalized,
            }
        )

    conflicting_texts = {text for text, labels in label_by_text.items() if len(labels) > 1}
    if conflicting_texts:
        safe_logger.info("dropping conflicting normalized texts", count=len(conflicting_texts))
    parsed = [row for row in parsed if row["normalized_text"] not in conflicting_texts]

    split_by_keyword = assign_keyword_splits([row["keyword"] for row in parsed], config)
    template_counts: Counter[tuple[str, str, str]] = Counter()
    exact_seen: set[tuple[str, str]] = set()
    candidates: dict[str, list[dict[str, Any]]] = {"train": [], "dev": [], "test": []}
    dropped_exact = 0
    dropped_template_cap = 0

    for row in parsed:
        split = split_by_keyword[row["keyword"]]
        exact_key = (row["label"], row["normalized_text"])
        if exact_key in exact_seen:
            dropped_exact += 1
            continue
        exact_seen.add(exact_key)
        masked = mask_keyword(row["text"], row["keyword"])
        template_key = (split, row["label"], masked)
        if template_counts[template_key] >= config.max_per_template:
            dropped_template_cap += 1
            continue
        template_counts[template_key] += 1
        row_id = stable_id(SOURCE_SEXHARM, row["keyword"], row["label"], row["normalized_text"])
        base = {
            "id": row_id,
            "text": row["text"],
            "normalized_text": row["normalized_text"],
            "original_text": None,
            "keyword": row["keyword"],
            "is_sexual_harmful": row["label"] == "Harmful",
            "sexual_category": "sexual_unspecified" if row["label"] == "Harmful" else "none",
            "context_type": "unknown",
            "evasion_types": ["none"],
            "evasion_spans": [],
            "source_id": SOURCE_SEXHARM,
            "source_url": "https://github.com/royal12646/Chinese-offensive-language-detect",
            "source_license": "Apache-2.0 on Hugging Face; Zenodo also lists CC BY 4.0; research-use disclaimer",
            "group_id": stable_id("keyword", normalize_text(row["keyword"])),
            "template_group_id": stable_id("template", masked),
            "split": split,
            "annotator_confidence": None,
            "notes": None,
            "is_augmented": False,
        }
        base.update(pinyin_features(base["text"]))
        candidates[split].append(base)

    # Leakage-safe filtering: dev cannot resemble train; test cannot resemble train/dev.
    near_index = NearDuplicateIndex(config.near_duplicate_hamming, config.near_duplicate_jaccard)
    filtered: dict[str, list[dict[str, Any]]] = {"train": [], "dev": [], "test": []}
    near_dropped: Counter[str] = Counter()
    for split in ("train", "dev", "test"):
        for row in sorted(candidates[split], key=lambda item: item["id"]):
            masked = mask_keyword(row["text"], row["keyword"])
            match = near_index.find(masked) if split != "train" else None
            if match is not None:
                near_dropped[split] += 1
                continue
            filtered[split].append(row)
            near_index.add(masked, row["id"])

    keyword_sets = {
        split: sorted({row["keyword"] for row in filtered[split]}) for split in filtered
    }
    stats = {
        "input_path": str(input_path),
        "input_sha256": sha256_file(input_path),
        "detected_encoding": encoding,
        "raw_rows": len(raw_rows),
        "invalid_rows": invalid,
        "conflicting_normalized_texts": len(conflicting_texts),
        "dropped_exact_duplicates": dropped_exact,
        "dropped_template_cap": dropped_template_cap,
        "dropped_cross_split_near_duplicates": dict(near_dropped),
        "output_rows": {split: len(rows) for split, rows in filtered.items()},
        "output_labels": {
            split: dict(Counter("Harmful" if row["is_sexual_harmful"] else "Safe" for row in rows))
            for split, rows in filtered.items()
        },
        "keyword_counts": {split: len(values) for split, values in keyword_sets.items()},
        "split_keyword_hashes": {
            split: stable_id(*values, length=32) if values else None
            for split, values in keyword_sets.items()
        },
    }
    return filtered, stats


def infer_evasion_types(original: str, perturbed: str) -> list[str]:
    if original == perturbed:
        return ["none"]
    types: set[str] = set()
    matcher = SequenceMatcher(a=original, b=perturbed, autojunk=False)
    for tag, a0, a1, b0, b1 in matcher.get_opcodes():
        if tag == "equal":
            continue
        left = original[a0:a1]
        right = perturbed[b0:b1]
        if tag == "replace" and len(left) == len(right):
            for source, target in zip(left, right):
                if source == target:
                    continue
                source_py = lazy_pinyin(source, style=Style.NORMAL, errors=lambda chars: list(chars))
                target_py = lazy_pinyin(target, style=Style.NORMAL, errors=lambda chars: list(chars))
                if source_py == target_py and source_py:
                    types.add("homophone_character")
                else:
                    types.add("other")
        elif tag in {"insert", "delete"}:
            types.add("character_split" if (left + right).strip() == "" else "other")
        else:
            types.add("other")
    return sorted(types) or ["other"]


def learn_homophone_map(pairs: Iterable[tuple[str, str]]) -> dict[str, list[dict[str, Any]]]:
    counts: dict[str, Counter[str]] = defaultdict(Counter)
    for original, perturbed in pairs:
        matcher = SequenceMatcher(a=original, b=perturbed, autojunk=False)
        for tag, a0, a1, b0, b1 in matcher.get_opcodes():
            if tag != "replace" or (a1 - a0) != (b1 - b0):
                continue
            for source, target in zip(original[a0:a1], perturbed[b0:b1]):
                if source == target or not source.strip() or not target.strip():
                    continue
                source_py = lazy_pinyin(source, style=Style.NORMAL, errors=lambda chars: list(chars))
                target_py = lazy_pinyin(target, style=Style.NORMAL, errors=lambda chars: list(chars))
                if source_py and source_py == target_py:
                    counts[source][target] += 1
    return {
        source: [
            {"char": target, "count": count}
            for target, count in counter.most_common()
        ]
        for source, counter in sorted(counts.items())
    }


def prepare_hed_pairs(raw_dir: Path) -> tuple[dict[str, list[dict[str, Any]]], dict[str, Any], dict[str, list[dict[str, Any]]]]:
    outputs: dict[str, list[dict[str, Any]]] = {}
    all_pairs: list[tuple[str, str]] = []
    stats: dict[str, Any] = {"splits": {}, "input_files": {}}
    for split in ("train", "dev", "test"):
        perturbed_path = raw_dir / "hed_cold" / f"{split}.csv"
        trace_path = raw_dir / "hed_cold" / "traceability" / f"sampled_{split}_data.csv"
        perturbed_rows, perturbed_encoding = read_csv_rows(perturbed_path)
        trace_rows, trace_encoding = read_csv_rows(trace_path)
        originals = {row["new_id"]: row for row in trace_rows}
        rows: list[dict[str, Any]] = []
        missing = 0
        for perturbed in perturbed_rows:
            original = originals.get(perturbed["id"])
            if original is None:
                missing += 1
                continue
            original_text = original["TEXT"].strip()
            perturbed_text = perturbed["TEXT"].strip()
            all_pairs.append((original_text, perturbed_text))
            evasion_types = infer_evasion_types(original_text, perturbed_text)
            row = {
                "id": stable_id(SOURCE_HED, split, perturbed["id"]),
                "source_record_id": perturbed["id"],
                "split": split,
                "topic": perturbed["topic"],
                "offensive_label": int(perturbed["label"]),
                "text": perturbed_text,
                "normalized_text": normalize_text(perturbed_text),
                "original_text": original_text,
                "original_normalized_text": normalize_text(original_text),
                "evasion_types": evasion_types,
                "source_id": SOURCE_HED,
                "source_url": "https://github.com/sjie320/HED_COLDataset",
                "source_license": "MIT",
                "group_id": stable_id("hed-original", original.get("original_id", perturbed["id"])),
            }
            row.update(pinyin_features(perturbed_text))
            original_features = pinyin_features(original_text)
            row.update({f"original_{key}": value for key, value in original_features.items()})
            rows.append(row)
        outputs[split] = rows
        stats["splits"][split] = {
            "rows": len(rows),
            "missing_pairs": missing,
            "labels": dict(Counter(row["offensive_label"] for row in rows)),
            "topics": dict(Counter(row["topic"] for row in rows)),
            "evasion_types": dict(Counter(value for row in rows for value in row["evasion_types"])),
        }
        stats["input_files"][str(perturbed_path)] = {
            "sha256": sha256_file(perturbed_path),
            "encoding": perturbed_encoding,
        }
        stats["input_files"][str(trace_path)] = {
            "sha256": sha256_file(trace_path),
            "encoding": trace_encoding,
        }
    homophone_map = learn_homophone_map(all_pairs)
    stats["learned_homophone_source_characters"] = len(homophone_map)
    stats["learned_homophone_edges"] = sum(len(values) for values in homophone_map.values())
    return outputs, stats, homophone_map


EMOJI_POOL = ("🌙", "🍑", "🔥", "💧", "🔞", "🌹")
SEPARATOR_POOL = ("·", "_", "-", "/")


def _replace_first(text: str, keyword: str, replacement: str) -> tuple[str, tuple[int, int] | None]:
    index = text.find(keyword)
    if index < 0:
        return text, None
    return text[:index] + replacement + text[index + len(keyword) :], (index, index + len(replacement))


def _normal_pinyin_compact(text: str) -> str:
    return "".join(lazy_pinyin(text, style=Style.NORMAL, errors=lambda chars: list(chars)))


def _initials_compact(text: str) -> str:
    return "".join(lazy_pinyin(text, style=Style.FIRST_LETTER, errors=lambda chars: list(chars)))


def create_augmentation(
    row: dict[str, Any],
    kind: str,
    homophone_map: dict[str, list[dict[str, Any]]],
) -> dict[str, Any] | None:
    text = row["text"]
    keyword = row["keyword"]
    replacement: str | None = None
    evasion_type = kind
    key_hash = int(hashlib.sha256(f"{row['id']}\0{kind}".encode("utf-8")).hexdigest(), 16)

    if kind == "homophone_character":
        chars = list(keyword)
        changed = False
        for index, char in enumerate(chars):
            options = homophone_map.get(char)
            if options and (key_hash >> index) & 1:
                chars[index] = options[0]["char"]
                changed = True
        if not changed:
            for index, char in enumerate(chars):
                options = homophone_map.get(char)
                if options:
                    chars[index] = options[0]["char"]
                    changed = True
                    break
        if changed:
            replacement = "".join(chars)
    elif kind == "pinyin_full":
        replacement = _normal_pinyin_compact(keyword)
    elif kind == "pinyin_initials":
        replacement = _initials_compact(keyword)
    elif kind == "symbol_insertion":
        separator = SEPARATOR_POOL[key_hash % len(SEPARATOR_POOL)]
        replacement = separator.join(keyword)
    elif kind == "character_split":
        replacement = " ".join(keyword)
    elif kind == "emoji":
        if keyword:
            position = key_hash % len(keyword)
            emoji = EMOJI_POOL[(key_hash // max(1, len(keyword))) % len(EMOJI_POOL)]
            replacement = keyword[:position] + emoji + keyword[position + 1 :]
    elif kind == "mixed_script":
        if keyword:
            position = key_hash % len(keyword)
            replacement = keyword[:position] + _normal_pinyin_compact(keyword[position]) + keyword[position + 1 :]
    else:
        return None

    if not replacement or replacement == keyword:
        return None
    augmented_text, span = _replace_first(text, keyword, replacement)
    if span is None or augmented_text == text:
        return None
    output = dict(row)
    output["id"] = stable_id(row["id"], kind, augmented_text)
    output["text"] = augmented_text
    output["normalized_text"] = normalize_text(augmented_text)
    output["original_text"] = text
    output["evasion_types"] = [evasion_type]
    output["evasion_spans"] = [
        {"start": span[0], "end": span[1], "type": evasion_type, "canonical": keyword}
    ]
    output["is_augmented"] = True
    output["augmentation_parent_id"] = row["id"]
    output.update(pinyin_features(augmented_text))
    return output


def augment_training_rows(
    rows: Sequence[dict[str, Any]],
    homophone_map: dict[str, list[dict[str, Any]]],
    config: BuildConfig,
) -> tuple[list[dict[str, Any]], dict[str, Any]]:
    kinds = (
        "homophone_character",
        "pinyin_full",
        "pinyin_initials",
        "symbol_insertion",
        "character_split",
        "emoji",
        "mixed_script",
    )
    outputs: list[dict[str, Any]] = []
    kind_counts: Counter[str] = Counter()
    skipped = 0
    for row in rows:
        rotation = int(row["id"][:8], 16) % len(kinds)
        ordered = kinds[rotation:] + kinds[:rotation]
        generated_for_row = 0
        seen_texts: set[str] = {row["normalized_text"]}
        for kind in ordered:
            augmented = create_augmentation(row, kind, homophone_map)
            if augmented is None or augmented["normalized_text"] in seen_texts:
                continue
            outputs.append(augmented)
            seen_texts.add(augmented["normalized_text"])
            kind_counts[kind] += 1
            generated_for_row += 1
            if generated_for_row >= config.augmentations_per_row:
                break
        if generated_for_row == 0:
            skipped += 1
    # Balance each evasion type independently so augmentation artifacts cannot
    # become a shortcut for the target label. Selection is deterministic.
    balanced: list[dict[str, Any]] = []
    balance_dropped: Counter[str] = Counter()
    for kind in kinds:
        harmful = sorted(
            (row for row in outputs if row["evasion_types"] == [kind] and row["is_sexual_harmful"]),
            key=lambda row: deterministic_rank(config.seed, row["id"]),
        )
        safe = sorted(
            (row for row in outputs if row["evasion_types"] == [kind] and not row["is_sexual_harmful"]),
            key=lambda row: deterministic_rank(config.seed, row["id"]),
        )
        target = min(len(harmful), len(safe))
        balanced.extend(harmful[:target])
        balanced.extend(safe[:target])
        balance_dropped[kind] = (len(harmful) - target) + (len(safe) - target)
    balanced.sort(key=lambda row: row["id"])
    balanced_kind_counts = Counter(row["evasion_types"][0] for row in balanced)
    return balanced, {
        "rows_before_balance": len(outputs),
        "rows": len(balanced),
        "parents": len(rows),
        "parents_without_augmentation": skipped,
        "types_before_balance": dict(kind_counts),
        "types": dict(balanced_kind_counts),
        "balance_dropped_by_type": dict(balance_dropped),
        "labels": dict(Counter("Harmful" if row["is_sexual_harmful"] else "Safe" for row in balanced)),
    }


def validate_no_group_leakage(splits: dict[str, Sequence[dict[str, Any]]]) -> dict[str, Any]:
    groups = {split: {row["group_id"] for row in rows} for split, rows in splits.items()}
    overlaps: dict[str, int] = {}
    ordered = ("train", "dev", "test")
    for left_index, left in enumerate(ordered):
        for right in ordered[left_index + 1 :]:
            overlaps[f"{left}_{right}"] = len(groups[left] & groups[right])
    return {"overlaps": overlaps, "passed": all(value == 0 for value in overlaps.values())}


def validate_no_exact_text_leakage(splits: dict[str, Sequence[dict[str, Any]]]) -> dict[str, Any]:
    values = {
        split: {row["normalized_text"] for row in rows}
        for split, rows in splits.items()
    }
    overlaps: dict[str, int] = {}
    ordered = ("train", "dev", "test")
    for left_index, left in enumerate(ordered):
        for right in ordered[left_index + 1 :]:
            overlaps[f"{left}_{right}"] = len(values[left] & values[right])
    return {"overlaps": overlaps, "passed": all(value == 0 for value in overlaps.values())}


def build_dataset(raw_dir: Path, output_dir: Path, config: BuildConfig) -> dict[str, Any]:
    safe_logger = SafeLogger()
    output_dir.mkdir(parents=True, exist_ok=True)
    sexharm_path = raw_dir / "zhatebench" / "SexHarmSet.csv"
    sex_splits, sex_stats = prepare_sexharm_rows(sexharm_path, config, safe_logger)
    hed_splits, hed_stats, homophone_map = prepare_hed_pairs(raw_dir)
    augmented_rows, augmentation_stats = augment_training_rows(sex_splits["train"], homophone_map, config)
    adversarial_config = replace(config, augmentations_per_row=7)
    synthetic_test_rows, synthetic_test_stats = augment_training_rows(
        sex_splits["test"], homophone_map, adversarial_config
    )

    output_counts: dict[str, int] = {}
    for split, rows in sex_splits.items():
        output_counts[f"sexharmset/{split}.jsonl"] = write_jsonl(output_dir / "sexharmset" / f"{split}.jsonl", rows)
    output_counts["sexharmset/train_augmented.jsonl"] = write_jsonl(
        output_dir / "sexharmset" / "train_augmented.jsonl", augmented_rows
    )
    output_counts["sexharmset/test_synthetic_adversarial.jsonl"] = write_jsonl(
        output_dir / "sexharmset" / "test_synthetic_adversarial.jsonl", synthetic_test_rows
    )
    for split, rows in hed_splits.items():
        output_counts[f"hed_cold_pairs/{split}.jsonl"] = write_jsonl(
            output_dir / "hed_cold_pairs" / f"{split}.jsonl", rows
        )
    write_json(output_dir / "homophone_map.json", homophone_map)

    leakage = validate_no_group_leakage(sex_splits)
    exact_text_leakage = validate_no_exact_text_leakage(sex_splits)
    if not leakage["passed"]:
        raise RuntimeError(f"Keyword group leakage detected: {leakage['overlaps']}")
    if not exact_text_leakage["passed"]:
        raise RuntimeError(f"Exact normalized-text leakage detected: {exact_text_leakage['overlaps']}")

    manifest = {
        "pipeline_version": "0.1.0",
        "config": asdict(config),
        "sexharmset": sex_stats,
        "hed_cold": hed_stats,
        "augmentation": augmentation_stats,
        "synthetic_adversarial_test": synthetic_test_stats,
        "validation": {
            "keyword_group_leakage": leakage,
            "exact_normalized_text_leakage": exact_text_leakage,
        },
        "output_counts": output_counts,
        "privacy": {
            "logs_contain_raw_text": False,
            "reports_contain_raw_text": False,
            "sample_references_are_sha256_prefixes": True,
        },
    }
    write_json(output_dir / "run_manifest.json", manifest)
    safe_logger.info(
        "dataset build completed",
        sex_rows=sum(len(rows) for rows in sex_splits.values()),
        augmented_rows=len(augmented_rows),
        hed_rows=sum(len(rows) for rows in hed_splits.values()),
    )
    return manifest
