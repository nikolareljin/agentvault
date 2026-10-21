# Built by script-helpers build_rpm_artifacts.sh with no prebuild, so Source0
# is `git archive HEAD` and %build compiles it; Go must be on PATH.
# _sourcedir is <repo>/packaging/rpm/build/SOURCES.
%global av_version %(tr -d '[:space:]' < %{_sourcedir}/../../../../VERSION)
%global debug_package %{nil}

Name:           agentvault
Version:        %{av_version}
Release:        1%{?dist}
Summary:        CLI and TUI for managing agent configurations and instructions
License:        MIT
URL:            https://github.com/nikolareljin/agentvault
Source0:        %{name}-%{version}.tar.gz

%description
Command-line and terminal UI tool that keeps AI agent configuration and
instruction files in one vault.

%prep
%setup -q

%build
CGO_ENABLED=0 go build -trimpath -mod=readonly \
  -ldflags "-s -w -X github.com/nikolareljin/agentvault/cmd.Version=%{version}" \
  -o agentvault .

%install
install -Dm0755 agentvault %{buildroot}%{_bindir}/agentvault

%files
%license LICENSE
%doc README.md CHANGELOG.md
%{_bindir}/agentvault
