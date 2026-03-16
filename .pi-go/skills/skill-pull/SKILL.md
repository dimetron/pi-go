---
name: skill-pull
description: Pull skills from a remote GitHub repository into the local skills directory. Use when the user wants to install shared skills.
---

# Skill Pull

Download and install skills from a remote source into `.pi-go/skills/`.

## Usage

    /skill-pull <source> [skill-name]

Where source can be:
- GitHub shorthand: `user/repo`
- GitHub URL: `https://github.com/user/repo`
- Raw URL to a SKILL.md file

If skill-name is provided, only pull that specific skill.

## Steps

1. Parse the source argument
2. For GitHub repos, use `git clone --depth 1` into a temp directory
3. Find skill directories: look for `skills/*/SKILL.md` or `.pi-go/skills/*/SKILL.md` in the cloned repo
4. For each found skill directory:
   - Copy the entire skill directory (SKILL.md + any bundled files) to `.pi-go/skills/<name>/`
   - If skill-name filter was given, only copy matching skills
   - If a skill with the same name exists locally, warn and skip unless the user confirms
5. Clean up temp directory
6. Report what was pulled

## Examples

- `/skill-pull user/repo` — Pull all skills from a GitHub repo
- `/skill-pull user/repo my-skill` — Pull only the my-skill skill
- `/skill-pull anthropics/skills` — Pull from the official Anthropic skills repo

## Guidelines

- Always clean up temp directories after cloning
- Never overwrite existing skills without user confirmation
- Report clearly what was installed and from where
