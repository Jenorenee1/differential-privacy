// Copyright 2020 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
#include "accounting/privacy_loss_distribution.h"

#include <algorithm>
#include <vector>

#include "absl/status/status.h"
#include "base/statusor.h"
#include "absl/strings/str_format.h"
#include "accounting/common/common.h"
#include "accounting/convolution.h"

namespace differential_privacy {
namespace accounting {

std::unique_ptr<PrivacyLossDistribution> PrivacyLossDistribution::Create(
    const ProbabilityMassFunction& pmf_lower,
    const ProbabilityMassFunction& pmf_upper, EstimateType estimate_type,
    double discretization_interval, double mass_truncation_bound) {
  double infinity_mass = 0;
  for (auto [outcome_upper, mass_upper] : pmf_upper) {
    if (pmf_lower.count(outcome_upper) == 0) {
      // When an outcome only appears in the upper distribution but not in the
      // lower distribution, it must be counted in infinity_mass as such an
      // outcome contributes to the hockey stick divergence.
      infinity_mass += mass_upper;
    }
  }
  // Compute non-discretized probability mass function for the PLD.
  ProbabilityMassFunctionOf<double> pmf;
  for (auto [outcome_lower, mass_lower] : pmf_lower) {
    if (pmf_upper.count(outcome_lower) != 0) {
      // This outcome is in both pmf_lower and pmf_upper.
      double logged_mass_lower = std::log(mass_lower);
      double logged_mass_upper = std::log(pmf_upper.at(outcome_lower));
      if (logged_mass_upper > mass_truncation_bound) {
        // Adding this to the distribution.
        double privacy_loss_value = logged_mass_upper - logged_mass_lower;
        pmf[privacy_loss_value] += pmf_upper.at(outcome_lower);
      } else if (estimate_type == EstimateType::kPessimistic) {
        // When the probability mass of mu_upper at the outcome is no more than
        // the threshold and we would like to get a pessimistic estimate,
        // account for this in infinity_mass.
        infinity_mass += pmf_upper.at(outcome_lower);
      }
    }
  }

  // Discretize the probability mass so that the values are integer multiples
  // of value_discretization_interval.
  ProbabilityMassFunction rounded_pmf;
  for (auto [outcome, mass] : pmf) {
    int key;
    if (estimate_type == EstimateType::kPessimistic) {
      key = static_cast<int>(std::ceil(outcome / discretization_interval));
    } else {
      key = static_cast<int>(std::floor(outcome / discretization_interval));
    }
    rounded_pmf[key] += mass;
  }

  return absl::WrapUnique(new PrivacyLossDistribution(
      discretization_interval, infinity_mass, rounded_pmf, estimate_type));
}

std::unique_ptr<PrivacyLossDistribution>
PrivacyLossDistribution::CreateIdentity(double discretization_interval) {
  return absl::WrapUnique(
      new PrivacyLossDistribution(discretization_interval,
                                  /*infinity_mass=*/0,
                                  /*probability_mass_function=*/{{0, 1}}));
}

std::unique_ptr<PrivacyLossDistribution>
PrivacyLossDistribution::CreateForAdditiveNoise(
    const AdditiveNoisePrivacyLoss& mechanism_privacy_loss,
    EstimateType estimate_type, double discretization_interval) {
  ProbabilityMassFunction pmf;

  auto round = [estimate_type](double x) {
    return estimate_type == EstimateType::kPessimistic ? ceil(x) : floor(x);
  };

  PrivacyLossTail tail = mechanism_privacy_loss.PrivacyLossDistributionTail();

  double infinity_mass = 0;
  for (auto [privacy_loss, probability_mass] : tail.probability_mass_function) {
    if (privacy_loss != std::numeric_limits<double>::infinity()) {
      double rounded_value = round(privacy_loss / discretization_interval);
      pmf[rounded_value] += probability_mass;
    } else {
      infinity_mass = probability_mass;
    }
  }

  if (mechanism_privacy_loss.Discrete() == NoiseType::kDiscrete) {
    for (int x = ceil(tail.lower_x_truncation);
         x <= floor(tail.upper_x_truncation); x++) {
      double rounded_value = round(mechanism_privacy_loss.PrivacyLoss(x) /
                                   discretization_interval);
      double probability_mass = mechanism_privacy_loss.NoiseCdf(x) -
                                mechanism_privacy_loss.NoiseCdf(x - 1);
      pmf[rounded_value] += probability_mass;
    }
  } else {
    double lower_x = tail.lower_x_truncation;
    double upper_x;
    double rounded_down_value = floor(
        mechanism_privacy_loss.PrivacyLoss(lower_x) / discretization_interval);
    while (lower_x < tail.upper_x_truncation) {
      upper_x = std::min(tail.upper_x_truncation,
                         mechanism_privacy_loss.InversePrivacyLoss(
                             discretization_interval * rounded_down_value));
      // Each x in [lower_x, upper_x] results in privacy loss that lies in
      // [discretization_interval * rounded_down_value,
      // discretization_interval * (rounded_down_value + 1)]
      double probability_mass = mechanism_privacy_loss.NoiseCdf(upper_x) -
                                mechanism_privacy_loss.NoiseCdf(lower_x);
      double rounded_value = round(rounded_down_value + 0.5);
      pmf[rounded_value] += probability_mass;

      lower_x = upper_x;
      rounded_down_value -= 1;
    }
  }

  return absl::WrapUnique(
      new PrivacyLossDistribution(discretization_interval, infinity_mass, pmf,
                                  mechanism_privacy_loss, estimate_type));
}

base::StatusOr<std::unique_ptr<PrivacyLossDistribution>>
PrivacyLossDistribution::CreateForRandomizedResponse(
    double noise_parameter, int num_buckets, EstimateType estimate_type,
    double discretization_interval) {
  if (noise_parameter <= 0 || noise_parameter >= 1) {
    return absl::InvalidArgumentError(absl::StrFormat(
        "Noise parameter %f should be strictly between 0 and 1.",
        noise_parameter));
  }
  if (num_buckets <= 1) {
    return absl::InvalidArgumentError(absl::StrFormat(
        "Number of buckets %i should be strictly greater than 1.",
        num_buckets));
  }

  ProbabilityMassFunction rounded_pmf;

  auto round = [estimate_type](double x) {
    return estimate_type == EstimateType::kPessimistic ? ceil(x) : floor(x);
  };

  // Probability that the output is equal to the input, i.e., Pr[R(x) = x]
  double probability_output_equal_input =
      (1 - noise_parameter) + noise_parameter / num_buckets;

  // Probability that the output is equal to a specific bucket that is not the
  // input, i.e., Pr[R(x') = x] for x' != x.
  double probability_output_not_input = noise_parameter / num_buckets;

  // Add privacy loss for the case o = x.
  double rounded_value = round(
      std::log(probability_output_equal_input / probability_output_not_input) /
      discretization_interval);
  rounded_pmf[rounded_value] += probability_output_equal_input;

  // Add privacy loss for the case o = x'
  rounded_value = round(
      std::log(probability_output_not_input / probability_output_equal_input) /
      discretization_interval);
  rounded_pmf[rounded_value] += probability_output_not_input;

  // Add privacy loss for the case o != x, x'
  rounded_pmf[0] += probability_output_not_input * (num_buckets - 2);

  return absl::WrapUnique(new PrivacyLossDistribution(
      discretization_interval,
      /*infinity_mass=*/0, rounded_pmf, estimate_type));
}

std::unique_ptr<PrivacyLossDistribution>
PrivacyLossDistribution::CreateForPrivacyParameters(
    EpsilonDelta epsilon_delta, double discretization_interval) {
  double epsilon = epsilon_delta.epsilon;
  double delta = epsilon_delta.delta;
  ProbabilityMassFunction rounded_pmf = {
      {ceil(epsilon / discretization_interval),
       (1 - delta) / (1 + std::exp(-epsilon))},
      {ceil(-epsilon / discretization_interval),
       (1 - delta) / (1 + std::exp(epsilon))},
  };

  return absl::WrapUnique(new PrivacyLossDistribution(
      discretization_interval, /*infinity_mass=*/delta, rounded_pmf));
}

absl::Status PrivacyLossDistribution::Compose(
    const PrivacyLossDistribution& other_pld, double tail_mass_truncation) {
  if (other_pld.DiscretizationInterval() != discretization_interval_) {
    return absl::InvalidArgumentError(absl::StrFormat(
        "Cannot compose, discretization intervals are different "
        "- %f vs %f",
        other_pld.DiscretizationInterval(), discretization_interval_));
  }

  double new_infinity_mass = infinity_mass_ + other_pld.InfinityMass() -
                             infinity_mass_ * other_pld.InfinityMass();

  if (estimate_type_ == EstimateType::kPessimistic) {
    // In the pessimistic case, the truncated probability mass needs to be
    // treated as if they were infinity.
    new_infinity_mass += tail_mass_truncation;
  }

  ProbabilityMassFunction new_pmf = Convolve(
      probability_mass_function_, other_pld.Pmf(), tail_mass_truncation);

  probability_mass_function_ = new_pmf;
  infinity_mass_ = new_infinity_mass;
  return absl::OkStatus();
}

void PrivacyLossDistribution::Compose(int num_times) {
  double new_infinity_mass = 1 - pow((1 - infinity_mass_), num_times);

  ProbabilityMassFunction new_pmf =
      Convolve(probability_mass_function_, num_times);

  probability_mass_function_ = new_pmf;
  infinity_mass_ = new_infinity_mass;
}

double PrivacyLossDistribution::GetDeltaForEpsilon(double epsilon) const {
  double divergence = infinity_mass_;
  for (auto [outcome, p] : probability_mass_function_) {
    auto val = outcome * discretization_interval_;
    if (val > epsilon && p > 0) {
      divergence += (1 - std::exp(epsilon - val)) * p;
    }
  }
  return divergence;
}

double PrivacyLossDistribution::GetEpsilonForDelta(double delta) const {
  if (infinity_mass_ > delta) return std::numeric_limits<double>::infinity();

  double mass_upper = infinity_mass_;
  double mass_lower = 0;

  // Sort outcomes in reverse order.
  std::vector<int> outcomes;
  for (auto [outcome, p] : probability_mass_function_) {
    outcomes.push_back(outcome);
  }
  std::sort(outcomes.rbegin(), outcomes.rend());

  for (int outcome : outcomes) {
    auto val = outcome * discretization_interval_;

    if (mass_upper - std::exp(val) * mass_lower >= delta) {
      // Epsilon is greater than or equal to val.
      break;
    }

    double pmf_val = probability_mass_function_.at(outcome);
    mass_upper += pmf_val;
    mass_lower += std::exp(-val) * pmf_val;

    if (mass_upper >= delta && mass_lower == 0) {
      // This only occurs when val is very large, which results in exp(-val)
      // being treated as zero.
      return std::max(0.0, val);
    }
  }

  if (mass_upper <= mass_lower + delta) return 0;

  return std::log((mass_upper - delta) / mass_lower);
}
}  // namespace accounting
}  // namespace differential_privacy
