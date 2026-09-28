# Homebrew formula TEMPLATE for AgentNet (ticket 4.4a). Do not edit the
# @PLACEHOLDERS@ by hand: render it from a SIGNATURE-VERIFIED SHA256SUMS with
#   sh packaging/homebrew/render.sh VERSION SHA256SUMS > agentnet.rb
# and commit the result to the tap repository (github.com/Magazem/homebrew-tap,
# Formula/agentnet.rb). See Docs/ops/release-signing.md.
#
# Homebrew users trust the tap repository: brew checks these SHA-256 values,
# not the release signature.
class Agentnet < Formula
  desc "Lets AI agents on different machines work together (Dorylinae)"
  homepage "https://github.com/Magazem/Dorylinae-Agentnet"
  version "@VERSION@"
  license "PolyForm-Shield-1.0.0"

  on_macos do
    on_arm do
      url "@BASE_URL@/agentnet_@VERSION@_darwin_arm64.tar.gz"
      sha256 "@SHA256_DARWIN_ARM64@"
    end
    on_intel do
      url "@BASE_URL@/agentnet_@VERSION@_darwin_amd64.tar.gz"
      sha256 "@SHA256_DARWIN_AMD64@"
    end
  end

  on_linux do
    on_arm do
      url "@BASE_URL@/agentnet_@VERSION@_linux_arm64.tar.gz"
      sha256 "@SHA256_LINUX_ARM64@"
    end
    on_intel do
      url "@BASE_URL@/agentnet_@VERSION@_linux_amd64.tar.gz"
      sha256 "@SHA256_LINUX_AMD64@"
    end
  end

  def install
    bin.install "agentnet", "agentnetd"
  end

  def caveats
    <<~EOS
      Next: agentnet setup
    EOS
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/agentnet --version")
    assert_match version.to_s, shell_output("#{bin}/agentnetd --version")
  end
end
