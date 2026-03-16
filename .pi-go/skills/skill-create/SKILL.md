---
name: skill-create
description: Create a new skill with guided setup. Use when the user wants to create a custom skill for pi-go.
---

# Skill Create

This skill is handled by the internal `/skill-create` command.

After the template is created, follow these steps:

## Step 1: Research

Do fast research on how similar tools/skills work:
- Search the codebase for related patterns, commands, or workflows
- Check existing skills in `.pi-go/skills/` for reference
- Identify what tools and steps are typically needed for this kind of task

## Step 2: Interview

Based on research, ask the user 1-3 focused questions:

A. What should this skill do and when? (one sentence)
B. What are the key steps? (or confirm the steps you found in research)
C. Anything specific to add or change?

## Step 3: Write

Update the SKILL.md with answers mapped to this structure:

    ---
    name: <skill-name>
    description: <answer A>
    ---

    # <Skill Name>

    <answer A expanded into clear instructions>

    ## Steps

    <answer B or researched steps, confirmed by user>

    ## Examples

    <concrete usage examples based on research + answers>

    ## Guidelines

    <answer C + best practices from research>
