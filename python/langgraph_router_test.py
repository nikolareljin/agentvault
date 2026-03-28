import io
import contextlib
import os
import sys
import unittest
from unittest import mock

sys.path.insert(0, os.path.dirname(__file__))

import langgraph_router


class LangGraphRouterMainTests(unittest.TestCase):
    def test_main_returns_clean_error_on_invalid_json_stdin(self) -> None:
        stderr = io.StringIO()
        with mock.patch("sys.stdin", io.StringIO("{")), contextlib.redirect_stderr(stderr):
            rc = langgraph_router.main()
        self.assertEqual(rc, 1)
        self.assertIn("Failed to read JSON payload from stdin", stderr.getvalue())

    def test_main_returns_clean_error_on_routing_failure(self) -> None:
        stderr = io.StringIO()
        payload = io.StringIO('{"prompt":"hi","config":{},"candidates":[]}')
        with (
            mock.patch("sys.stdin", payload),
            mock.patch("langgraph_router._run_with_langgraph", side_effect=ImportError("missing langgraph")),
            contextlib.redirect_stderr(stderr),
        ):
            rc = langgraph_router.main()
        self.assertEqual(rc, 1)
        self.assertIn("Routing failed: no candidates provided", stderr.getvalue())


if __name__ == "__main__":
    unittest.main()
