# Template: scripts/update-tap.sh fills in @VERSION@ and @SHA256@.
class RexMicro < Formula
  desc "Use a Work Louder Codex Micro as a controller for the Rex terminal"
  homepage "https://github.com/JiehoonKwak/rex-micro"
  url "https://github.com/JiehoonKwak/rex-micro/releases/download/v@VERSION@/rex-micro_@VERSION@_darwin_universal.tar.gz"
  version "@VERSION@"
  sha256 "@SHA256@"
  license "MIT"

  depends_on :macos

  def install
    bin.install "rex-micro"
  end

  def caveats
    <<~EOS
      Start rex-micro now and at login (once; upgrades apply themselves):
        rex-micro service install

      Use this rather than `brew services`: macOS ties the Input Monitoring
      permission to the executable's path, and the service keeps one fixed path
      across upgrades.

      macOS then asks to let rex-micro receive keystrokes. Allow it under
      System Settings > Privacy & Security > Input Monitoring, and check with:
        rex-micro doctor

      Quit Work Louder Input. The ChatGPT desktop app also drives the pad
      while it has Input Monitoring: switch ChatGPT off there and restart it.
      Shortcut bindings also need Accessibility for rex-micro.
    EOS
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/rex-micro version")
    system bin/"rex-micro", "config", "check"
  end
end
