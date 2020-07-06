# mc-rcon
This is an implementation of the Minecraft RCON protcol in Go, based off of [this spec](https://wiki.vg/RCON). It is a CLI program with support for [color output](https://minecraft.gamepedia.com/Formatting_codes).


Help menu
```
$ mc-rcon 
Usage: mc_rcon [OPTIONS...] HOST
  -no-color
        disable color output
  -port string
        port number (default "25575")
```

Example connection
```
$ mc-rcon example.com
Password: 
Successfully logged in
> time set day
Set the time to 1000 
> ^C
Shutting down... 
```

Enjoy :)