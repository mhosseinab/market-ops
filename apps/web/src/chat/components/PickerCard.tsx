import { useT } from "../../app/i18n";
import { LtrToken } from "../../components/LtrToken";
import type { PickerOption } from "../types";
import { DeepLinkButton } from "./DeepLinkButton";

// Ambiguity picker (CHAT-007 / §16 multiple-matching-variants): a request that
// could lead to a card ALWAYS shows a structured picker first — no action card is
// created directly. Selecting an option is navigation/refinement (a deep link),
// never an approval. Nothing here mutates.
export function PickerCard({ options }: { options: readonly PickerOption[] }) {
  const t = useT();
  return (
    <section className="chat-card chat-picker" data-testid="chat-picker">
      <p className="chat-card__title">{t("chat.picker.title")}</p>
      <p className="chat-card__hint">{t("chat.picker.hint")}</p>
      <ul className="chat-picker__options">
        {options.map((option) => (
          <li key={option.id} className="chat-picker__option">
            <span className="chat-picker__label">{option.label}</span>
            {option.sku ? <LtrToken text={option.sku} /> : null}
            {option.deepLink ? (
              <DeepLinkButton
                link={option.deepLink}
                labelKey="chat.picker.select"
                testId="chat-picker-select"
              />
            ) : null}
          </li>
        ))}
      </ul>
    </section>
  );
}
