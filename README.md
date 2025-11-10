# `obs-alsa-auto-reopen`

When an ALSA input is not reliable, OBS sometimes loose it and does not retry to reopen it.
So, I made this ugly service to force OBS to reopen it.

In my case the command is something like this:
```sh
go run ./ --input 'DIY mic (ALSA)' --input 'Mic (ALSA)' --pass myFancySecretPassword
```
