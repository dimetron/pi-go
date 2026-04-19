"""Entry point for the hello world example."""


def greet(name: str) -> str:
    """Return a friendly greeting for the given name."""
    return f"Hello, {name}!"


def main() -> None:
    """Print a greeting to stdout."""
    print(greet("world"))


if __name__ == "__main__":
    main()
