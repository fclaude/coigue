# Security

If you find a security problem in Coigüe, please tell me privately before you tell anyone else. GitHub has a way to do that: open the Security tab of this repository and choose Report a vulnerability. I'll answer as soon as I can, and I'd like to fix the problem before it's described in public.

What I most want to hear about is a way for something you didn't write to run Scheme in the editor: a file next to the one you open, a repository, or a file's own contents. The extension language is only meant to run what's in your own configuration. A command that reaches past the few operations it was given, such as the file system, a process or the network, is a problem as well, and so is anything that corrupts your file or leaves the terminal in a bad state.

Only `main` and the latest tagged version get fixes.
