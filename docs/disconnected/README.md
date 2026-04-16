# Disconnected Installation

This directory contains `ImageSetConfiguration` files for mirroring the
Trustee operator in disconnected (air-gapped) environments using `oc-mirror`.

## What is covered

Each `imageset-config-<ocp-version>.yaml` file mirrors:

- The Trustee operator from the Red Hat operator catalog, including the
  catalog index, operator bundle, and operator controller image.
- The KBS operand image, listed as `relatedImages` in the operator bundle
  and automatically picked up by `oc-mirror`.

One file is provided per supported OCP version.

## Usage

1. Select the file matching your OCP version, e.g. `imageset-config-4.17.yaml`.

2. Edit the `imageURL` field to point to your internal registry:

   ```yaml
   storageConfig:
     registry:
       imageURL: <your-registry>/mirror/oc-mirror-metadata
   ```

3. Run `oc-mirror` to mirror all images to your internal registry:

   ```bash
   oc-mirror --config imageset-config-4.17.yaml docker://<your-registry>
   ```

4. Apply the generated IDMS (ImageDigestMirrorSet) / ICSP (ImageContentSourcePolicy)
   manifests to your cluster. These remap image references from upstream
   registries to your internal mirror:

   ```bash
   oc apply -f oc-mirror-workspace/results-*/
   ```

5. Install the operator using the mirrored catalog as the source.
