from __future__ import annotations

from dataclasses import dataclass
from typing import Sequence


@dataclass(frozen=True)
class ThresholdPolicy:
    pass_threshold: float
    block_threshold: float
    target_block_precision: float
    target_pass_recall: float


def confusion(labels: Sequence[int], probabilities: Sequence[float], threshold: float) -> dict[str, int]:
    tp = fp = tn = fn = 0
    for label, probability in zip(labels, probabilities):
        predicted = int(probability >= threshold)
        if label == 1 and predicted == 1:
            tp += 1
        elif label == 0 and predicted == 1:
            fp += 1
        elif label == 0 and predicted == 0:
            tn += 1
        else:
            fn += 1
    return {"tp": tp, "fp": fp, "tn": tn, "fn": fn}


def binary_metrics(labels: Sequence[int], probabilities: Sequence[float], threshold: float = 0.5) -> dict[str, float | int]:
    counts = confusion(labels, probabilities, threshold)
    tp, fp, tn, fn = counts["tp"], counts["fp"], counts["tn"], counts["fn"]
    total = max(1, tp + fp + tn + fn)
    precision = tp / max(1, tp + fp)
    recall = tp / max(1, tp + fn)
    specificity = tn / max(1, tn + fp)
    f1 = 2 * precision * recall / max(1e-12, precision + recall)
    result: dict[str, float | int] = {
        **counts,
        "threshold": threshold,
        "accuracy": (tp + tn) / total,
        "precision": precision,
        "recall": recall,
        "specificity": specificity,
        "f1": f1,
        "balanced_accuracy": 0.5 * (recall + specificity),
    }
    return result


def average_precision(labels: Sequence[int], probabilities: Sequence[float]) -> float:
    positives = sum(labels)
    if positives == 0:
        return 0.0
    ranked = sorted(zip(probabilities, labels), key=lambda item: (-item[0], -item[1]))
    tp = 0
    precision_sum = 0.0
    for rank, (_, label) in enumerate(ranked, start=1):
        if label:
            tp += 1
            precision_sum += tp / rank
    return precision_sum / positives


def choose_threshold_policy(
    labels: Sequence[int],
    probabilities: Sequence[float],
    target_block_precision: float = 0.9,
    target_pass_recall: float = 0.98,
) -> ThresholdPolicy:
    candidates = sorted({round(index / 200, 3) for index in range(1, 200)} | {round(p, 6) for p in probabilities})
    best_f1_threshold = max(candidates, key=lambda value: float(binary_metrics(labels, probabilities, value)["f1"]))

    block_candidates: list[tuple[float, float, float]] = []
    for threshold in candidates:
        metrics = binary_metrics(labels, probabilities, threshold)
        if metrics["precision"] >= target_block_precision and (metrics["tp"] + metrics["fp"]) > 0:
            block_candidates.append((float(metrics["recall"]), float(metrics["precision"]), threshold))
    if block_candidates:
        _, _, block_threshold = max(block_candidates, key=lambda item: (item[0], item[1], -item[2]))
    else:
        block_threshold = best_f1_threshold

    # Passing means probability < pass_threshold. Choose the largest threshold
    # that still preserves the requested positive recall after automatic passing.
    pass_candidates: list[float] = []
    for threshold in candidates:
        metrics = binary_metrics(labels, probabilities, threshold)
        if metrics["recall"] >= target_pass_recall:
            pass_candidates.append(threshold)
    pass_threshold = max(pass_candidates) if pass_candidates else min(0.2, block_threshold * 0.5)
    if pass_threshold >= block_threshold:
        pass_threshold = max(0.01, block_threshold * 0.5)
    return ThresholdPolicy(
        pass_threshold=float(pass_threshold),
        block_threshold=float(block_threshold),
        target_block_precision=target_block_precision,
        target_pass_recall=target_pass_recall,
    )


def policy_metrics(
    labels: Sequence[int], probabilities: Sequence[float], policy: ThresholdPolicy
) -> dict[str, float | int | dict[str, int]]:
    actions = {"pass": 0, "review": 0, "block": 0}
    unsafe_actions = {"pass": 0, "review": 0, "block": 0}
    safe_actions = {"pass": 0, "review": 0, "block": 0}
    for label, probability in zip(labels, probabilities):
        if probability < policy.pass_threshold:
            action = "pass"
        elif probability >= policy.block_threshold:
            action = "block"
        else:
            action = "review"
        actions[action] += 1
        (unsafe_actions if label else safe_actions)[action] += 1
    total = max(1, len(labels))
    return {
        "actions": actions,
        "unsafe_actions": unsafe_actions,
        "safe_actions": safe_actions,
        "pass_coverage": actions["pass"] / total,
        "review_coverage": actions["review"] / total,
        "block_coverage": actions["block"] / total,
        "unsafe_auto_pass_rate": unsafe_actions["pass"] / max(1, sum(labels)),
        "safe_auto_block_rate": safe_actions["block"] / max(1, len(labels) - sum(labels)),
    }
