"""ASGI entrypoint: ``uvicorn llm.asgi:app``.

``create_app()`` builds the FastAPI app fresh from ``load_settings()`` (env
``LLM_*`` vars; the OpenAI-compatible mock provider by default — no paid
calls, §12.5). This module just exposes the module-level ``app`` object
uvicorn's CLI expects, so a compose service or a production container can run
the plane with one command. Settings — including a real provider endpoint —
are entirely environment-driven; nothing here is dev-only or test-only.
"""

from llm.app import create_app

app = create_app()
