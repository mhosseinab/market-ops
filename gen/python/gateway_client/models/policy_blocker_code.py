from enum import Enum


class PolicyBlockerCode(str, Enum):
    BOUNDARY_INVALID = "boundary_invalid"
    BOUNDARY_UNKNOWN = "boundary_unknown"
    CONTRIBUTION_BELOW_FLOOR = "contribution_below_floor"
    CONTRIBUTION_CROSSES_ZERO = "contribution_crosses_zero"
    COOLDOWN_ACTIVE = "cooldown_active"
    MOVEMENT_CAP_INFEASIBLE = "movement_cap_infeasible"
    OBJECTIVE_INFEASIBLE = "objective_infeasible"
    STRATEGY_DISABLED = "strategy_disabled"

    def __str__(self) -> str:
        return str(self.value)
