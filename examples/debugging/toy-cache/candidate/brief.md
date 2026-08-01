# The {{.app_name}} cache does not answer

Build {{.build_id}} went out and the cache stopped serving. Nothing else
changed, or so the deploy log claims.

You have kubectl against one namespace, `toy-{{.app_name}}`. The service is
called `cache` and it should return the nginx welcome page.

Find out what is wrong and fix it. Narrate as you go: what you think is
happening, what you checked, and what changed your mind. Say when you are
guessing.

This is an example problem shipped with the platform, so it is small on
purpose. A real one would not be.
