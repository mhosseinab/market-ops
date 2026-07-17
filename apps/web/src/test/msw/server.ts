import { setupServer } from "msw/node";
import { handlers } from "./handlers";

// Shared MSW node server for component tests. Lifecycle is wired in test/setup.ts;
// unhandled requests error so a screen that hits an unmocked endpoint fails loud.
export const server = setupServer(...handlers);
