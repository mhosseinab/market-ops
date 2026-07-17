import type { MessageKey } from "@market-ops/locale";
import type { ReactNode } from "react";
import { useT } from "../app/i18n";

// Generic RTL data table (component inventory). Header text resolves through the
// catalog; every cell uses `text-align:start` so RTL text and LTR identifier
// columns mix cleanly. Selection + row click are optional. This is presentation
// only — sorting/filtering happen in the screen against API data.

export interface Column<Row> {
  readonly id: string;
  readonly header: MessageKey;
  readonly render: (row: Row) => ReactNode;
  readonly align?: "start" | "end";
}

export function DataTable<Row>({
  columns,
  rows,
  rowKey,
  onRowClick,
  selectedId,
  caption,
}: {
  columns: readonly Column<Row>[];
  rows: readonly Row[];
  rowKey: (row: Row) => string;
  onRowClick?: (row: Row) => void;
  selectedId?: string;
  caption?: MessageKey;
}) {
  const t = useT();
  return (
    <div className="data-table__wrap">
      <table className="data-table">
        {caption ? <caption className="data-table__caption">{t(caption)}</caption> : null}
        <thead>
          <tr>
            {columns.map((col) => (
              <th key={col.id} style={{ textAlign: col.align ?? "start" }}>
                {t(col.header)}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => {
            const key = rowKey(row);
            const selected = selectedId === key;
            return (
              <tr
                key={key}
                className="data-table__row"
                data-selected={selected ? "true" : undefined}
                data-clickable={onRowClick ? "true" : undefined}
                onClick={onRowClick ? () => onRowClick(row) : undefined}
              >
                {columns.map((col) => (
                  <td key={col.id} style={{ textAlign: col.align ?? "start" }}>
                    {col.render(row)}
                  </td>
                ))}
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
