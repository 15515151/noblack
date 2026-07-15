from __future__ import annotations

from collections.abc import Iterable, Mapping
from typing import Any

ACTION_RANK = {"pass": 0, "review": 1, "block": 2}
SUPPORTED_COMBINE_POLICIES = {"consensus", "max"}


def combine_model_actions(
    results: Iterable[Mapping[str, Any]],
    policy: str = "consensus",
) -> str:
    """Combine independent model actions without letting one noisy model dominate.

    ``consensus`` is precision-oriented: both models must be non-pass before the
    aggregate result is review/block, and both must independently block before
    the aggregate result is block. ``max`` preserves the legacy most-strict
    behavior for deployments that explicitly prefer recall over precision.
    """
    normalized_policy = policy.strip().lower()
    if normalized_policy not in SUPPORTED_COMBINE_POLICIES:
        allowed = ", ".join(sorted(SUPPORTED_COMBINE_POLICIES))
        raise ValueError(f"unsupported combine policy {policy!r}; expected one of: {allowed}")

    actions = [str(result.get("action", "")).strip().lower() for result in results]
    if not actions:
        raise ValueError("at least one model result is required")
    invalid = sorted({action for action in actions if action not in ACTION_RANK})
    if invalid:
        raise ValueError(f"unsupported model action(s): {', '.join(invalid)}")

    if normalized_policy == "max":
        return max(actions, key=ACTION_RANK.__getitem__)

    if all(action == "block" for action in actions):
        return "block"
    if all(action != "pass" for action in actions):
        return "review"
    return "pass"
