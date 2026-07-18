import { useLocale, useT } from "../../app/i18n";
import { formatCount } from "../../data/format";
import type { InlineTable } from "../types";
import { DeepLinkButton } from "./DeepLinkButton";

// Inline result table with the CHAT-023 20-row rule: the parser already caps rows
// at 20; when the query matched more, the view summarizes the remainder and
// deep-links to the structured screen instead of dumping unbounded rows. Header
// and cell text are grounded query DATA, isolated per-cell.
export function InlineTableView({ table }: { table: InlineTable }) {
  const t = useT();
  const { locale } = useLocale();
  const truncated = table.totalRows > table.rows.length;

  return (
    <div className="chat-table" data-testid="chat-table" data-total-rows={table.totalRows}>
      <table className="chat-table__grid">
        {table.headers.length > 0 ? (
          <thead>
            <tr>
              {table.headers.map((h, i) => (
                // biome-ignore lint/suspicious/noArrayIndexKey: static positional table header
                <th key={`${i}:${h}`} scope="col">
                  {h}
                </th>
              ))}
            </tr>
          </thead>
        ) : null}
        <tbody>
          {table.rows.map((row, r) => (
            // biome-ignore lint/suspicious/noArrayIndexKey: static positional table row
            <tr key={`${r}:${row.join("|")}`}>
              {row.map((cell, c) => (
                // biome-ignore lint/suspicious/noArrayIndexKey: static positional table cell
                <td key={`${r}:${c}:${cell}`}>{cell}</td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
      {truncated ? (
        <div className="chat-table__more" data-testid="chat-table-truncated">
          <p className="chat-table__summary">
            {t("chat.table.summary", {
              shown: formatCount(table.rows.length, locale),
              total: formatCount(table.totalRows, locale),
            })}
          </p>
          {table.deepLink ? (
            <DeepLinkButton
              link={table.deepLink}
              labelKey="chat.table.deepLink"
              testId="chat-table-deeplink"
            />
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
