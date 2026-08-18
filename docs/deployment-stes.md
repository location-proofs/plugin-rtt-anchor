## Anchor
1. On the anchor, create allowlist.txt
2. Get public key from attester. Attester key is attained by running `attester -print-key` on the attester itself.
3. On the anchor, update allowlist.txt to include the hex key of the attester.
4. Make sure you have the longitude and latitude values for the anchor.
5. Make sure the port you want to use on the anchor is open, in this case it is port 8922.
6. Run `anchor -id NAME-FOR-ANCHOR -lat 60.34290113194924 -lng 25.030340378449665 -listen 0.0.0.0:8922 -key ./key.json -allowlist ./allowlist.txt`

## Attester
1. Get the anchor IP and port. In this case it is 37.27.88.255:8922 for our VPS example.
2. Get the anchor key, this is printed/logged by the anchor as `public_key=54defdbcfe0a6099ab04737a403b680b4b7775267a4647451c91173433caa4a7` in this case.
3. Finally run `attester -anchor 37.27.88.255:8922 -anchor-key 54defdbcfe0a6099ab04737a403b680b4b7775267a4647451c91173433caa4a7`

If you are having issues, make sure the keys are correct between the attester and anchor.
Make sure the ports are available on each end, both the anchor and the attester.