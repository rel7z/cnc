"""Run an unchanged Python entrypoint and capture its selected text directory."""
import json
from pathlib import Path
import subprocess
import sys
import threading
from processor_publish import Publisher


class Monitor:
    def __init__(self, publisher, watch, excluded, outputs=None):
        self.publisher = publisher
        self.watches = [Path(path) for path in (watch if isinstance(watch, list) else [watch])]
        self.excluded = {Path(path).absolute() for path in excluded}
        self.errors = {}
        self.outputs = outputs

    def capture(self):
        current_errors = {}
        paths = set()
        for watch in self.watches:
            try:
                if watch.is_symlink():
                    raise ValueError("watch directory must not be a symlink")
                if self.outputs is None:
                    paths.update(watch.iterdir())
                else:
                    for name in self.outputs:
                        relative = Path(name)
                        if relative.is_absolute() or ".." in relative.parts:
                            raise ValueError("output escapes script directory")
                        path = watch
                        for part in relative.parts:
                            path = path / part
                            if path.is_symlink():
                                current_errors[str(path)] = "output must not contain symlinks"
                                break
                        else:
                            paths.add(path)
            except (OSError, ValueError) as error:
                current_errors[str(watch)] = str(error)
        for path in sorted(paths):
            if self.outputs is not None and path.exists() and not path.is_file():
                current_errors[str(path)] = "output must be a regular file"
                continue
            if (path.suffix != ".txt" or path.name.startswith(".") or
                    path.absolute() in self.excluded or path.is_symlink() or not path.is_file()):
                continue
            try:
                self.publisher.publish(path, deliver=False)
            except FileNotFoundError:
                continue  # A result was removed between discovery and capture.
            except (OSError, ValueError) as error:
                current_errors[str(path)] = str(error)
        for path, error in current_errors.items():
            if self.errors.get(path) != error:
                print(f"automatic publication error: {path}: {error}", file=sys.stderr, flush=True)
        self.errors = current_errors


def main(config):
    publisher = Publisher()
    monitor = Monitor(publisher, config["watch"], config["excluded"], config.get("outputs"))
    stop = threading.Event()

    def deliver():
        while not stop.is_set():
            try:
                publisher.drain()
            except OSError as error:
                print(f"automatic publication transfer pending: {error}", file=sys.stderr, flush=True)
            stop.wait(1)

    sender = threading.Thread(target=deliver, daemon=True)
    sender.start()
    child = subprocess.Popen(config.get("command") or [sys.executable, config["entrypoint"], *config.get("args", [config["input"], config["output"]])])
    try:
        while child.poll() is None:
            monitor.capture()
            try:
                child.wait(timeout=1)
            except subprocess.TimeoutExpired:
                pass
        monitor.capture()
    finally:
        stop.set()
        if child.poll() is None:
            child.terminate()
            try:
                child.wait(timeout=5)
            except subprocess.TimeoutExpired:
                child.kill()
                child.wait()
        sender.join(timeout=6)
    # The worker performs durable outbox delivery before reporting completion.
    if monitor.errors:
        print("automatic publication failed for final output; see errors above", file=sys.stderr, flush=True)
        return child.returncode or 65
    return child.returncode


if __name__ == "__main__":
    raise SystemExit(main(json.loads(sys.argv[1])))
