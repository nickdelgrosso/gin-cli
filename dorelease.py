# Copyright (c) 2017, German Neuroinformatics Node (G-Node)
#
# All rights reserved.
#
# Redistribution and use in source and binary forms, with or without
# modification, are permitted under the terms of the BSD License. See
# LICENSE file in the root of the Project.
"""
Build gin-cli binaries and package them for distribution.
"""
import pickle
import sys
import os
import shutil
import json
import re
from glob import glob
from subprocess import check_output, call, DEVNULL
from tempfile import TemporaryDirectory
import requests
import plistlib
from requests.exceptions import ConnectionError as ConnError

DESTDIR = "dist"
PKGDIR = os.path.join(DESTDIR, "pkg")

ETAGFILE = os.path.join(DESTDIR, "etags")
ETAGS = {}

VERSION = {}


def run(cmd, **kwargs):
    print(f"> {' '.join(cmd)}")
    return call(cmd, **kwargs)


def load_etags():
    """
    Read in etags file and populates dictionary.
    """
    try:
        with open(ETAGFILE, "rb") as etagfile:
            ETAGS.update(pickle.load(etagfile))
    except FileNotFoundError:
        # print("--> No etags file found. Skipping load.")
        pass


def save_etags():
    """
    Save (potentially new) etags to file.
    """
    with open(ETAGFILE, "wb") as etagfile:
        pickle.dump(ETAGS, etagfile)


def download(url, fname=None):
    """
    Download a URL if necessary. If the URL's etag matches the existing one,
    the download is skipped.
    """
    if fname is None:
        fname = url.split("/")[-1]
    fname = os.path.join(DESTDIR, "downloads", fname)
    print(f"--> Downloading {url} → {fname}")
    try:
        req = requests.get(url, stream=True)
    except ConnError:
        print(f"Error while trying to download {url}", file=sys.stderr)
        print("Skipping.", file=sys.stderr)
        return
    size = int(req.headers.get("content-length"))
    etag = req.headers.get("etag")
    oldet = ETAGS.get(url)
    if etag == oldet and os.path.exists(fname):
        fod_size = os.path.getsize(fname)
        if fod_size == size:
            print("File already downloaded. Skipping.", end="\n\n")
            return fname
    ETAGS[url] = etag
    prog = 0
    with open(fname, "wb") as dlfile:
        for chunk in req.iter_content(chunk_size=1048576):
            dlfile.write(chunk)
            prog += len(chunk)
            print(f"\r{prog/size*100:2.1f}%", end="", flush=True)
        print("\nDone!")
    print()
    return fname


def die(msg):
    """
    Exit the program with a given error message and exit status 1.
    """
    print(msg, file=sys.stderr)
    sys.exit(1)


def wait_for_ret():
    """
    Pause execution and wait for the user to hit return. If ctrl+c (interrupt)
    is received instead, exit the program with status 1.
    """
    try:
        input("Hit return to continue or ^C to cancel ...")
    except KeyboardInterrupt:
        die("\nCancelled")


def build():
    """
    Build binaries.
    """
    platforms = ["linux/amd64", "windows/386", "windows/amd64", "darwin/amd64"]
    print(f"--> Building binary for {', '.join(platforms)}")
    verfilename = "version"
    with open(verfilename) as verfile:
        verinfo = verfile.read()

    VERSION["version"] = re.search(r"version=(.*)", verinfo).group(1)
    cmd = ["git", "rev-list", "--count", "HEAD"]
    VERSION["build"] = int(check_output(cmd).strip().decode())
    cmd = ["git", "rev-parse", "HEAD"]
    VERSION["commit"] = check_output(cmd).strip().decode()
    print(("Version: {version} "
           "Build: {build:06d} "
           "Commit: {commit}").format(**VERSION))
    ldflags = ("-X main.gincliversion={version} "
               "-X main.build={build:06d} "
               "-X main.commit={commit}").format(**VERSION)
    output = os.path.join(DESTDIR, "{{.OS}}-{{.Arch}}", "gin")
    cmd = [
        "gox", f"-output={output}",
        f"-osarch={' '.join(platforms)}", f"-ldflags={ldflags}"
    ]
    print(f"Running {' '.join(cmd)}")
    if run(cmd) > 0:
        die("Build failed")

    print()
    print("--> Build succeeded")
    print("--> The following files were built:")
    ginfiles = glob(os.path.join(DESTDIR, "*", "gin"))
    ginfiles.extend(glob(os.path.join(DESTDIR, "*", "gin.exe")))
    print("\n".join(ginfiles), end="\n\n")

    plat = sys.platform
    for ginbin in ginfiles:
        if plat in ginbin:
            cmd = [ginbin, "--version"]
            verstring = check_output(cmd).strip().decode()
            print(f"{' '.join(cmd)}\n➥ {verstring}")
    print()
    return ginfiles


def download_annex_sa():
    """
    Download annex standaline tarball.
    """
    annex_sa_url = ("https://downloads.kitenet.net/git-annex/linux/current/"
                    "git-annex-standalone-amd64.tar.gz")
    return download(annex_sa_url)


def check_macos_tarball():
    """
    Checks if git-annex tarball is in the download location
    """
    path = "./dist/downloads/git-annex-latest-macos.tar.bz2"
    if os.path.exists(path):
        print(f"Found {path}")
        return path
    print(f"macOS git-annex archive {path} not found")
    return None


def get_git_for_windows():
    """
    Download the (portable) git for windows package.  Relies on github API to
    find latest release.  Downloads all files that match "*PortableGit*" which
    should include both 32 and 64 bit versions.
    """
    url = "https://api.github.com/repos/git-for-windows/git/releases/latest"
    req = requests.get(url)
    releases = json.loads(req.text)
    assets = releases["assets"]

    downloaded = list()
    for asset in assets:
        if "PortableGit" in asset["name"]:
            win_git_url = asset["browser_download_url"]
            downloaded.append(download(win_git_url))
    return downloaded


def get_git_annex_for_windows():
    """
    Download the git annex for windows installer.
    """
    # win_git_annex_url = ("https://downloads.kitenet.net/git-annex/windows/"
    #                      "current/git-annex-installer.exe")
    # return download(win_git_annex_url)
    # fixing windows installer version (again)
    fname = os.path.join(DESTDIR, "downloads", "git-annex-installer.exe")
    return fname


def package_linux_plain(binfile):
    """
    For each Linux binary make a tarball and include all related files.
    """
    dirname, fname = os.path.split(binfile)
    _, osarch = os.path.split(dirname)
    # simple binary archive
    shutil.copy("README.md", dirname)
    arc = f"gin-cli-{VERSION['version']}-{osarch}.tar.gz"
    archive = os.path.join(PKGDIR, arc)
    cmd = ["tar", "-czf", archive, "-C", dirname, fname, "README.md"]
    print(f"Running {' '.join(cmd)}")
    if run(cmd) > 0:
        print(f"Failed to make tarball for {binfile}", file=sys.stderr)
        return None
    return archive


def debianize(binfile, annexsa_archive):
    """
    For each Linux binary make a deb package with git annex standalone.
    """
    def docker_cleanup():
        print("Stopping and cleaning up docker container")
        cmd = ["docker", "kill", "gin-deb-build"]
        run(cmd)
        cmd = ["docker", "container", "rm", "gin-deb-build"]
        run(cmd)

    docker_cleanup()

    # The default temporary root on macOS is /var/folders
    # Docker currently has issues mounting directories under /var
    # Forcing temporary directory to be rooted at /tmp instead
    tmpprefix = None
    if sys.platform == "darwin":
        tmpprefix = "/tmp/"
    with TemporaryDirectory(prefix=tmpprefix, suffix="gin-linux") as tmpdir:
        cmd = ["docker", "build", "-t", "gin-deb", "debdock/."]
        print("Preparing docker image for debian build")
        run(cmd)

        # debian packaged with annex standalone
        # create directory structure
        # pkg gin-cli-version
        # /opt
        # /opt/gin/
        # /opt/gin/git-annex.linux/...
        # /opt/gin/bin/gin (binary)
        # /opt/gin/bin/gin.sh (shell script for running gin cmds)
        # /usr/local/gin -> /opt/gin/bin/gin.sh (symlink)

        # put build script in container build directory
        shutil.copy(os.path.join("scripts", "makedeb"), tmpdir)

        # create directory structure
        pkgname = "gin-cli"
        pkgnamever = f"{pkgname}-{VERSION['version']}"
        debmdsrc = os.path.join("debdock", "debian")
        pkgdir = os.path.join(tmpdir, pkgname)
        debcapdir = os.path.join(pkgdir, "DEBIAN")
        opt_dir = os.path.join(pkgdir, "opt")
        opt_gin_dir = os.path.join(opt_dir, "gin")
        opt_gin_bin_dir = os.path.join(opt_gin_dir, "bin")
        usr_local_bin_dir = os.path.join(pkgdir, "usr", "local", "bin")
        docdir = os.path.join(pkgdir, "usr", "share", "doc", pkgname)

        os.makedirs(debcapdir)
        os.makedirs(opt_gin_bin_dir)
        os.makedirs(usr_local_bin_dir)
        os.makedirs(docdir)

        # copy binaries and program files
        shutil.copy(binfile, opt_gin_bin_dir)
        print(f"Copied {binfile} to {opt_gin_bin_dir}")
        shutil.copy(os.path.join("scripts", "gin.sh"), opt_gin_bin_dir)
        print(f"Copied gin.sh to {opt_gin_bin_dir}")

        link_path = os.path.join(usr_local_bin_dir, "gin")
        os.symlink("/opt/gin/bin/gin.sh", link_path)

        shutil.copy("README.md", opt_gin_dir)

        # copy debian package metadata files
        shutil.copy(os.path.join(debmdsrc, "control"), debcapdir)
        shutil.copy("LICENSE", os.path.join(docdir, "copyright"))
        shutil.copy(os.path.join(debmdsrc, "changelog"), docdir)
        shutil.copy(os.path.join(debmdsrc, "changelog.Debian"), docdir)

        # TODO: Update changelog automatically
        # Adding version number to debian control file
        controlpath = os.path.join(debcapdir, "control")
        with open(controlpath) as controlfile:
            controllines = controlfile.read().format(**VERSION)

        with open(controlpath, "w") as controlfile:
            controlfile.write(controllines)

        # gzip changelog and changelog.Debian
        cmd = [
            "gzip", "--best",
            os.path.join(docdir, "changelog"),
            os.path.join(docdir, "changelog.Debian")
        ]
        if run(cmd) > 0:
            print(f"Failed to gzip files in {docdir}", file=sys.stderr)

        # extract annex standalone into pkg/opt/gin
        cmd = ["tar", "-xzf", annexsa_archive, "-C", opt_gin_dir]
        print(f"Running {' '.join(cmd)}")
        if run(cmd) > 0:
            print(f"Failed to extract {annexsa_archive} to {opt_gin_dir}",
                  file=sys.stderr)
            return None

        contdir = "/debbuild/"
        cmd = [
            "docker", "run", "-it",  "--rm", "-v", f"{tmpdir}:{contdir}",
            "--name", "gin-deb-build", "gin-deb"
        ]
        print("Running debian build script")
        if run(cmd) > 0:
            print("Deb build failed", file=sys.stderr)
            docker_cleanup()
            return

        debfilename = f"{pkgname}.deb"
        debfilepath = os.path.join(tmpdir, debfilename)
        debfiledest = os.path.join(PKGDIR, f"{pkgnamever}.deb")
        if os.path.exists(debfiledest):
            os.remove(debfiledest)
        shutil.copy(debfilepath, debfiledest)
        print("Done")
        docker_cleanup()
    return debfiledest


def rpmify(binfiles, annexsa_archive):
    """
    For each Linux binary make a rpm package with git annex standalone.
    """
    return []


def package_mac_plain(binfile):
    """
    For each Darwin binary make a tarball and include all related files.
    """
    dirname, fname = os.path.split(binfile)
    _, osarch = os.path.split(dirname)
    osarch = osarch.replace("darwin", "macos")
    # simple binary archive
    shutil.copy("README.md", dirname)
    archive = f"gin-cli-{VERSION['version']}-{osarch}.tar.gz"
    archive = os.path.join(PKGDIR, archive)
    cmd = ["tar", "-czf", archive, "-C", dirname, fname, "README.md"]
    print(f"Running {' '.join(cmd)}")
    if run(cmd) > 0:
        print(f"Failed to make tarball for {binfile}", file=sys.stderr)
        return None
    return archive


def package_mac_bundle(binfile, annex_tar):
    """
    For each macOS binary make a zip that includes the annex.app with the gin
    binary in its path.
    """
    with TemporaryDirectory(suffix="gin-macos") as tmpdir:
        # extract macOS git-annex tar into pkgroot
        cmd = ["tar", "-xjf", annex_tar, "-C", tmpdir]
        print(f"Running {' '.join(cmd)}")
        if run(cmd, stdout=DEVNULL) > 0:
            print(f"Failed to extract {annex_tar} to {tmpdir}",
                  file=sys.stderr)
            return None

        annexapproot = os.path.join(tmpdir, "git-annex.app")
        pkgroot = os.path.join(tmpdir, "gin-cli")
        ginapproot = os.path.join(pkgroot, "gin-cli.app")
        os.mkdir(pkgroot)

        # move only git-annex.app and LICENSE.txt to pkgroot
        shutil.move(annexapproot, ginapproot)
        shutil.move(os.path.join(tmpdir, "LICENSE.txt"),
                    os.path.join(pkgroot, "git-annex-LICENSE.txt"))

        macosdir = os.path.join(ginapproot, "Contents", "MacOS")
        bindir = os.path.join(macosdir, "bundle")
        shutil.copy(binfile, bindir)
        shutil.copy("README.md", os.path.join(pkgroot, "GIN-README.md"))

        # remove git-annex icon
        os.remove(os.path.join(ginapproot, "Contents", "Resources",
                               "git-annex.icns"))
        # TODO: Add GIN icon

        with open("./macapp/gin-Info.plist", "rb") as plistfile:
            info = plistlib.load(plistfile, fmt=plistlib.FMT_XML)
            info["CFBundleVersion"] = VERSION["version"]
            info["CFBundleShortVersionString"] = VERSION["version"]
            # info["CFBundleExecutable"] = "runshell"
        with open(os.path.join(ginapproot, "Contents", "Info.plist"),
                  "wb") as plistfile:
            plistlib.dump(info, plistfile, fmt=plistlib.FMT_XML)

        dirname, _ = os.path.split(binfile)
        _, osarch = os.path.split(dirname)
        osarch = osarch.replace("darwin", "macos")

        archive = f"gin-cli-{VERSION['version']}-{osarch}-bundle.tar.gz"
        archive = os.path.join(PKGDIR, archive)
        print("Creating macOS bundle")
        if os.path.exists(archive):
            os.remove(archive)
        arc_abs = os.path.abspath(archive)

        # rename git-annex LICENSE and add gin license
        shutil.copy("./LICENSE", os.path.join(pkgroot, "LICENSE.txt"))

        # same for README
        os.rename(os.path.join(macosdir, "README"),
                  os.path.join(macosdir, "git-annex-README"))
        shutil.copy("./README.md", os.path.join(macosdir, "README"))

        # add launch script
        shutil.copy("scripts/launch-macos.sh",
                    os.path.join(macosdir, "launch"))

        # create the archive
        cmd = ["tar", "-czf", arc_abs, "-C", pkgroot, "."]
        print(f"Running {' '.join(cmd)} (from {pkgroot})")
        if run(cmd, stdout=DEVNULL) > 0:
            print(f"Failed to create archive {archive} in {pkgroot}",
                  file=sys.stderr)
            return None
        print("DONE")
    return archive


def winbundle(binfile, git_pkg, annex_pkg):
    """
    For each Windows binary make a zip and include git and git annex portable
    """
    with TemporaryDirectory(suffix="gin-windows") as tmpdir:
        pkgroot = os.path.join(tmpdir, "gin")
        bindir = os.path.join(pkgroot, "bin")
        os.makedirs(bindir)

        shutil.copy(binfile, bindir)
        shutil.copy("README.md", pkgroot)
        shutil.copy(os.path.join("scripts", "gin-shell.bat"), pkgroot)

        gitdir = os.path.join(pkgroot, "git")
        os.makedirs(gitdir)

        # extract git portable and annex into git dir
        cmd = ["7z", "x", f"-o{gitdir}", git_pkg]
        print(f"Running {' '.join(cmd)}")
        if run(cmd, stdout=DEVNULL) > 0:
            print(f"Failed to extract git archive {git_pkg} to {gitdir}",
                  file=sys.stderr)
            return None

        cmd = ["7z", "x", f"-o{gitdir}", annex_pkg]
        print(f"Running {' '.join(cmd)}")
        if run(cmd, stdout=DEVNULL) > 0:
            print(f"Failed to extract git archive {annex_pkg} to {gitdir}",
                  file=sys.stderr)
            return None
        dirname, _ = os.path.split(binfile)
        _, osarch = os.path.split(dirname)

        arc = f"gin-cli-{VERSION['version']}-{osarch}.zip"
        arc = os.path.join(PKGDIR, arc)
        print("Creating Windows zip file")
        # need to change paths before making zip file
        if os.path.exists(arc):
            os.remove(arc)
        arc_abs = os.path.abspath(arc)
        oldwd = os.getcwd()
        os.chdir(pkgroot)
        cmd = ["zip", "-r", arc_abs, "."]
        print(f"Running {' '.join(cmd)}")
        if run(cmd, stdout=DEVNULL) > 0:
            print(f"Failed to create archive {arc}", file=sys.stderr)
            os.chdir(oldwd)
            return None
        os.chdir(oldwd)
    print("DONE")
    return arc


def main():
    """
    Main
    """
    os.makedirs(os.path.join(DESTDIR, "downloads"), exist_ok=True)
    os.makedirs(PKGDIR, exist_ok=True)

    # build binaries
    binfiles = build()

    # download stuff
    load_etags()
    annexsa_file = download_annex_sa()
    win_git_files = get_git_for_windows()
    win_git_annex_file = get_git_annex_for_windows()
    mac_annex_tar = check_macos_tarball()
    save_etags()

    if len(win_git_files) != 2:
        print("Need two Git archives for Windows")
        print(f"Got {len(win_git_files)}")
        print("Aborting")
        sys.exit(1)

    print("Ready to package")

    linux_bins = [b for b in binfiles if "linux" in b]
    win_bins = [b for b in binfiles if "windows" in b]
    darwin_bins = [b for b in binfiles if "darwin" in b]

    if len(linux_bins) != 1 or len(win_bins) != 2 or len(darwin_bins) != 1:
        print("Unexpected number of binaries")
        print(f"Linux: {len(linux_bins)}")
        print(f"Windows: {len(win_bins)}")
        print(f"macOS: {len(darwin_bins)}")

    # package stuff
    linux_pkg = package_linux_plain(linux_bins[0])
    deb_pkg = debianize(linux_bins[0], annexsa_file)

    rpm_pkg = rpmify(linux_bins[0], annexsa_file)

    mac_pkg = package_mac_plain(darwin_bins[0])
    mac_bundle = package_mac_bundle(darwin_bins[0], mac_annex_tar)

    win_bin_32 = [wb for wb in win_bins if "386" in wb][0]
    win_bin_64 = [wb for wb in win_bins if "amd64" in wb][0]
    win_git_32 = [wg for wg in win_git_files if "32-bit" in wg][0]
    win_git_64 = [wg for wg in win_git_files if "64-bit" in wg][0]

    win_pkg_32 = winbundle(win_bin_32, win_git_32, win_git_annex_file)
    win_pkg_64 = winbundle(win_bin_64, win_git_64, win_git_annex_file)

    def link_latest(fname):
        """
        Create symlinks with the version part replaced by 'latest' for the
        newly built package.
        """
        latestname = fname.replace(VERSION["version"], "latest")
        print(f"Linking {fname} to {latestname}")
        if os.path.lexists(latestname):
            os.unlink(latestname)
        os.link(fname, latestname)

    # print info
    print("------------------------------------------------")
    print("The following archives and packages were created")
    print("------------------------------------------------")
    if linux_pkg:
        print("Linux tarball:")
        print(">", linux_pkg)
        link_latest(linux_pkg)

    if deb_pkg:
        print("Debian package:")
        print(">", deb_pkg)
        link_latest(deb_pkg)

    if rpm_pkg:
        print("RPM package:")
        print(">", rpm_pkg)
        link_latest(rpm_pkg)

    if mac_pkg:
        print("macOS package:")
        print(">", mac_pkg)
        link_latest(mac_pkg)
    if mac_bundle:
        print("macOS bundle:")
        print(mac_bundle)
        link_latest(mac_bundle)

    if win_pkg_32:
        print("Windows 32-bit bundle:")
        print(">", win_pkg_32)
        link_latest(win_pkg_32)

    if win_pkg_64:
        print("Windows 64-bit bundle:")
        print(">", win_pkg_64)
        link_latest(win_pkg_64)


if __name__ == "__main__":
    main()
