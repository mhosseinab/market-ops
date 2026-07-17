from enum import Enum


class ConnectorCapability(str, Enum):
    BOUNDARY_READ = "boundary_read"
    BUYBOX_READ = "buybox_read"
    CATALOG_READ = "catalog_read"
    CHANGE_FEED = "change_feed"
    COMMISSION_READ = "commission_read"
    OWNED_OFFER_READ = "owned_offer_read"
    PRICE_WRITE = "price_write"
    SALES_CONTEXT_READ = "sales_context_read"
    STOCK_READ = "stock_read"

    def __str__(self) -> str:
        return str(self.value)
