# Privacy Policy — Omega Network Engineers Tools

Applies to OmegaSSH, OmegaCat, OmegaMaps and OmegaMaps Viewer, distributed together
as "Omega Network Engineers Tools" on the Microsoft Store.

Last updated: September 19, 2026

## Summary

The tools run entirely on your computer. They do not collect, transmit, sell or share
personal information, and they contain no telemetry, analytics, advertising or
crash reporting.

## Data stored on your computer

- **Credentials** you enter (usernames, passwords, SSH keys, SNMP communities) are kept
  in an encrypted vault file in your user profile (`~/.omega`, `~/.omegacat`,
  `~/.omegamaps`). The vault is encrypted with a master password you choose. If you
  opt in, that master password is stored in the Windows Credential Manager.
- **Sessions, captures, maps and settings** are stored as files in your user profile
  or in folders you choose.

This data never leaves your computer unless you copy or share the files yourself.
Uninstalling the app does not delete these files; remove the folders above to erase them.

## Network connections

The tools connect only to the network devices you specify, using SSH and SNMP, to
run sessions, capture device state and discover topology. Credentials are sent only
to those devices, as part of authenticating to them. No data is sent to the developer
or to any third party.

## Children

The tools are not directed at children and collect no information from anyone.

## Changes

Changes to this policy will be posted at this URL with a new "Last updated" date.

## Contact

Questions: open an issue at https://github.com/scottpeterman/omegacatqt/issues
