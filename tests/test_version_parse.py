from cockpit.version_parse import parse_version, compare


def test_parse_plain_semver():
    assert parse_version("2.1.101") == "2.1.101"


def test_parse_embedded():
    assert parse_version("claude 2.1.98 (Claude Code)") == "2.1.98"
    assert parse_version("v0.9.0") == "0.9.0"


def test_parse_with_custom_regex():
    assert parse_version("image: multica:0.8.2", r"multica:([0-9.]+)") == "0.8.2"


def test_parse_none():
    assert parse_version("no version here") is None


def test_parse_custom_regex_no_group():
    # 自訂 regex 無 capture group → 回整段 match，不崩潰
    assert parse_version("image: app:1.2.3", r"app:[0-9.]+") == "app:1.2.3"


def test_parse_invalid_regex_returns_none():
    assert parse_version("whatever 1.2.3", r"([0-9") is None


def test_compare():
    assert compare("2.1.98", "2.1.101") == ("behind", 3)
    assert compare("2.1.101", "2.1.101") == ("up_to_date", 0)
    assert compare("1.0.0", "0.9.0") == ("up_to_date", 0)   # 本地比上游新 → 視為最新
    assert compare(None, "2.1.101") == ("unknown", 0)
