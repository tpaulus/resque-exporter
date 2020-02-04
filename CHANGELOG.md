# Changelog

## 0.3.0

#### Changed

* Run as user `nobody` and group `nogroup` instead of `root`.

## 0.2.2

#### Fixed

* Stop missing `stat` keys from preventing other metrics from being scraped.

## 0.2.1

#### Changed

* Support scaling to zero workers when using `resque_processing_ratio`.

## 0.2.0

#### Added

* Add `resque_processing_ratio` metric.
* Add `resque_workers_per_queue` metric.

#### Changed

* Rename the repository to `resque-exporter`.

## 0.1.0

* Initial release.
