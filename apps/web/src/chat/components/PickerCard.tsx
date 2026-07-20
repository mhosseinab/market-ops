import { useT } from "../../app/i18n";
import { LtrToken } from "../../components/LtrToken";
import { useChatDockActions } from "../dockActions";
import type { PickerOption } from "../types";
import { DeepLinkButton } from "./DeepLinkButton";

// Ambiguity picker (CHAT-007 / §16 multiple-matching-variants): a request that
// could lead to a card ALWAYS shows a structured picker first — no action card is
// created directly. Selecting an option BINDS that exact option to the
// conversation through a typed, versioned continuation turn (an explicit context
// transition) BEFORE any card-producing request, so the conversation is never
// silently relabeled and the bound entity is the option the operator chose.
// Selecting never approves; the binding carries no authority. When rendered
// without a dock (fallback), selection falls back to deep-link navigation.
export function PickerCard({ options }: { options: readonly PickerOption[] }) {
  const t = useT();
  const actions = useChatDockActions();
  return (
    <section className="chat-card chat-picker" data-testid="chat-picker">
      <p className="chat-card__title">{t("chat.picker.title")}</p>
      <p className="chat-card__hint">{t("chat.picker.hint")}</p>
      <ul className="chat-picker__options">
        {options.map((option) => (
          <li key={option.id} className="chat-picker__option">
            <span className="chat-picker__label">{option.label}</span>
            {option.sku ? <LtrToken text={option.sku} /> : null}
            {actions ? (
              <button
                type="button"
                className="chat-picker__select"
                data-testid="chat-picker-select"
                onClick={() => actions.bindPickerOption(option)}
              >
                {t("chat.picker.select")}
              </button>
            ) : option.deepLink ? (
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
