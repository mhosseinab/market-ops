from enum import Enum


class ObservationRoute(str, Enum):
    ROUTE_A = "route_a"
    ROUTE_B = "route_b"
    ROUTE_C = "route_c"

    def __str__(self) -> str:
        return str(self.value)
