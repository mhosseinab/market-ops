import { Link } from "@tanstack/react-router";
import { useT } from "../app/i18n";
import { NAV_GROUPS, ROUTES } from "../app/navConfig";
import { LtrToken } from "./LtrToken";

// Right-hand nav (RTL → rightmost). Primary "workspace" group + "reference"
// group; active item highlighted. Labels resolve through the catalog. Chat is
// deliberately NOT a nav item — it is a dock layer.
export function SideNav() {
  const t = useT();
  return (
    <nav className="side-nav" aria-label={t("nav.group.workspace")}>
      <strong>
        <LtrToken text={t("brand.mark")} />
      </strong>
      {NAV_GROUPS.map((group) => (
        <div key={group.id}>
          <div className="side-nav__group-label">{t(group.labelKey)}</div>
          {ROUTES.filter((r) => r.navGroup === group.id).map((r) => (
            <Link
              key={r.key}
              to={r.path}
              className="nav-item"
              activeProps={{ "data-active": "true" }}
            >
              {r.navLabelKey ? t(r.navLabelKey) : null}
            </Link>
          ))}
        </div>
      ))}
    </nav>
  );
}
